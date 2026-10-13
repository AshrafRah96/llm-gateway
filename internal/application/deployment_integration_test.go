//go:build deployment

package application

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/ashrafrah96/llm-gateway/internal/auth"
	"github.com/ashrafrah96/llm-gateway/internal/redisconfig"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHardenedLocalDeployment(t *testing.T) {
	dir := os.Getenv("HARDENED_TEST_DIR")
	if dir == "" {
		t.Fatal("HARDENED_TEST_DIR must point to deployment-init output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	options := func(service, role, addr string) *redis.Options {
		values := map[string]string{"REDIS_ADDR": addr, "REDIS_USERNAME": role, "REDIS_PASSWORD_FILE": filepath.Join(dir, service+"_"+role), "REDIS_TLS": "true", "REDIS_TLS_CA": filepath.Join(dir, "ca.crt"), "REDIS_TLS_SERVER_NAME": service}
		if service == "cache" {
			values["REDIS_TLS_SERVER_NAME"] = "redis"
		}
		o, err := redisconfig.Options(func(k string) string { return values[k] }, "REDIS_", addr)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	cacheRuntime := options("cache", "gateway", "127.0.0.1:16380")
	ledgerRuntime := options("ledger", "gateway", "127.0.0.1:16381")
	operator := redis.NewClient(options("ledger", "operator", "127.0.0.1:16381"))
	defer operator.Close()
	runtime := redis.NewClient(ledgerRuntime)
	defer runtime.Close()
	pepper, err := os.ReadFile(filepath.Join(dir, "pepper"))
	if err != nil {
		t.Fatal(err)
	}
	project, key := "llmgw-local-restore-proof", "fixture-development-key"
	hash := sha256.Sum256([]byte(project))
	p := fmt.Sprintf("ledger:{%x}:", hash)
	iterator := operator.Scan(ctx, 0, p+"*", 100).Iterator()
	for iterator.Next(ctx) {
		if err := operator.Del(ctx, iterator.Val()).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := iterator.Err(); err != nil {
		t.Fatal(err)
	}
	store := auth.NewKeyStore(operator, string(pepper))
	record := "project_key:" + store.Fingerprint(key)
	operator.Del(ctx, record)
	if err := store.Provision(ctx, project, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.HSet(ctx, record, "project", "attacker").Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("runtime could administer keys: %v", err)
	}
	cfg := Config{OpenAIAPIKey: "fixture-no-paid-provider-key", RedisAddr: cacheRuntime.Addr, LedgerAddr: ledgerRuntime.Addr, RedisOptions: cacheRuntime, LedgerOptions: ledgerRuntime, CacheTTL: time.Hour, APIKeyPepper: string(pepper), RequestTimeout: time.Minute, MaxRequestBodyBytes: 64 << 10, MaxConcurrentRequests: 16, MaxConcurrentPerProject: 4, Router: router.Default()}
	app, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, path := range []string{"/health", "/ready", "/models", "/usage"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-API-Key", key)
		w := httptest.NewRecorder()
		app.Server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	tracker := usage.NewTracker(runtime)
	if err := tracker.SetLimits(ctx, project, 999_999, 999_999); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("runtime could change budget policy: %v", err)
	}
	if err := usage.NewTracker(operator).SetLimits(ctx, project, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	known, err := tracker.Reserve(ctx, project, "known-fixture", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Settle(ctx, known, usage.Entry{Kind: "chat", TokensIn: 3, TokensOut: 1, CostNanos: 10}, true); err != nil {
		t.Fatal(err)
	}
	unknown, err := tracker.Reserve(ctx, project, "unknown-fixture", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := tracker.Settle(ctx, unknown, usage.Entry{Kind: "chat", TokensIn: 3, CostNanos: 10, Estimated: true}, false); err != nil {
		t.Fatal(err)
	}
	audit, err := tracker.AuditTotals(ctx, project, false)
	if err != nil || !audit.Match || audit.Events != 2 {
		t.Fatalf("audit: %+v %v", audit, err)
	}
	bad := *ledgerRuntime
	bad.Username = "default"
	bad.Password = ""
	bad.MaxRetries = -1
	bad.DialTimeout = 200 * time.Millisecond
	unauth := redis.NewClient(&bad)
	defer unauth.Close()
	if err := unauth.Ping(ctx).Err(); err == nil {
		t.Fatal("unauthenticated access succeeded")
	}
	bad = *ledgerRuntime
	bad.TLSConfig = ledgerRuntime.TLSConfig.Clone()
	bad.TLSConfig.ServerName = "wrong-hostname"
	bad.MaxRetries = -1
	bad.DialTimeout = 200 * time.Millisecond
	untrusted := redis.NewClient(&bad)
	defer untrusted.Close()
	if err := untrusted.Ping(ctx).Err(); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
	// Leave only this explicitly named synthetic project for the isolated restore drill.
	t.Log("Authenticated TLS, scoped ACLs, private stores, dependency readiness and synthetic restore fixture verified; no provider calls.")
}
