package auth

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyStore is a thin adapter over Redis, so there is nothing to verify without one.
// Dialled once for the package so an absent Redis costs one timeout, not one per test.
var dial = sync.OnceValues(func() (*redis.Client, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("no redis at %s: %w", addr, err)
	}
	return client, nil
})

func store(t *testing.T) (*KeyStore, *redis.Client) {
	t.Helper()

	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	// Seeding goes through the client, not the store: provisioning is somebody else's
	// job, and Valid is the whole interface.
	client.Del(context.Background(), keyName)
	t.Cleanup(func() { client.Del(context.Background(), keyName) })

	return NewKeyStore(client), client
}

func TestKeyStore_Valid(t *testing.T) {
	s, client := store(t)
	ctx := context.Background()

	if err := client.SAdd(ctx, keyName, "live-key").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"live-key", true},
		{"unknown-key", false},
		{"", false},
		{"LIVE-KEY", false}, // keys are compared exactly, not case-insensitively
	} {
		got, err := s.Valid(ctx, tt.key)
		if err != nil {
			t.Fatalf("Valid(%q): %v", tt.key, err)
		}
		if got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestKeyStore_ValidWithNoKeysConfigured(t *testing.T) {
	s, _ := store(t)

	got, err := s.Valid(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("an empty key set must not authorise anything")
	}
}

func TestKeyStore_StoresOnlyFingerprintAndReturnsProjectPrincipal(t *testing.T) {
	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	store := NewKeyStore(client, "pepper")
	ctx := context.Background()
	raw := "project-secret-key"
	key := projectKeyPrefix + store.Fingerprint(raw)
	client.Del(ctx, key)
	t.Cleanup(func() { client.Del(ctx, key) })

	if err := store.Provision(ctx, "project-a", raw); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	data, err := client.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	for _, value := range data {
		if value == raw {
			t.Fatal("raw API key was stored in Redis")
		}
	}
	principal, ok, err := store.Lookup(ctx, raw)
	if err != nil || !ok {
		t.Fatalf("Lookup = %+v, %v, %v", principal, ok, err)
	}
	if principal.ProjectID != "project-a" || principal.KeyID == raw {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestKeyLifecyclePreservesIdentity(t *testing.T) {
	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	s := NewKeyStore(client, "lifecycle-pepper")
	project := "lifecycle-project"
	keys := []string{"legacy-lifecycle", "new-lifecycle", "replacement-lifecycle", "revoked-lifecycle"}
	for _, key := range keys {
		client.Del(ctx, projectKeyPrefix+s.Fingerprint(key))
	}
	t.Cleanup(func() {
		for _, key := range keys {
			client.Del(ctx, projectKeyPrefix+s.Fingerprint(key))
		}
	})
	client.HSet(ctx, projectKeyPrefix+s.Fingerprint(keys[0]), "project", project, "revoked", "0")
	if _, ok, err := s.Lookup(ctx, keys[0]); err != nil || !ok {
		t.Fatalf("legacy invalid: %v %v", ok, err)
	}
	if err := s.Provision(ctx, project, keys[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, "other-project", keys[1]); err == nil {
		t.Fatal("reassigned key")
	}
	now, err := client.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	expiry := client.HGet(ctx, projectKeyPrefix+s.Fingerprint(keys[1]), "expires_at").Val()
	if expiry == "" {
		t.Fatal("new key lacks expiry")
	}
	if err := s.Rotate(ctx, project, keys[0], keys[2], time.Hour, 90*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys[:3] {
		p, ok, err := s.Lookup(ctx, key)
		if err != nil || !ok || p.ProjectID != project {
			t.Fatalf("overlap %s: %+v %v %v", key, p, ok, err)
		}
	}
	client.HSet(ctx, projectKeyPrefix+s.Fingerprint(keys[0]), "expires_at", now.Unix())
	if _, ok, err := s.Lookup(ctx, keys[0]); err != nil || ok {
		t.Fatalf("expired valid: %v %v", ok, err)
	}
	if err := s.Provision(ctx, project, keys[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Lookup(ctx, keys[0]); ok {
		t.Fatal("expired key revived by provisioning")
	}
	if err := s.Provision(ctx, project, keys[3]); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, project, keys[3]); err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(ctx, project, keys[3]); err == nil {
		t.Fatal("revoked key reprovisioned")
	}
	if _, ok, _ := s.Lookup(ctx, keys[3]); ok {
		t.Fatal("revoked key valid")
	}
}
