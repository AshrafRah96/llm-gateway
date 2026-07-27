package cache

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func redisSearchClient(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	t.Cleanup(func() { client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis is not reachable: %v", err)
	}
	if _, err := client.Do(ctx, "FT._LIST").Result(); err != nil {
		t.Skipf("Redis Search is not available: %v", err)
	}
	return client
}

func newIntegrationCache(t *testing.T, client *redis.Client, embedder Embedder, ttl time.Duration) *SemanticCache {
	t.Helper()
	c, err := NewSemanticCache(client, embedder, ttl)
	if err != nil {
		t.Fatalf("NewSemanticCache: %v", err)
	}
	return c
}

func integrationNamespace(t *testing.T, model string) Namespace {
	t.Helper()
	return NewNamespace("integration-"+t.Name(), model)
}

func TestSemanticCacheEquivalentPromptHitsWithinNamespace(t *testing.T) {
	client := redisSearchClient(t)
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, time.Hour)
	ns := integrationNamespace(t, "cheap-model")

	if err := c.Set(context.Background(), ns, "What is France's capital?", []byte(`{"answer":"Paris"}`), 200); err != nil {
		t.Fatalf("Set: %v", err)
	}
	stored, err := client.HGetAll(context.Background(), cacheKey(ns, "What is France's capital?")).Result()
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	for field, value := range stored {
		if field == "embedding" {
			continue
		}
		if strings.Contains(value, "integration-"+t.Name()) {
			t.Fatalf("field %q stored the raw API key", field)
		}
	}
	got, err := c.Get(context.Background(), ns, "Name the capital city of France")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || string(got.Response) != `{"answer":"Paris"}` {
		all, searchErr := client.Do(context.Background(),
			"FT.SEARCH", indexName, "*",
			"RETURN", "4", "tenant", "model", "version", "data",
		).Result()
		t.Fatalf("Get = %+v, want the cached Paris response; unfiltered search = %#v, err = %v",
			got, all, searchErr)
	}
}

func TestSemanticCacheDoesNotCrossTenantModelOrVersion(t *testing.T) {
	client := redisSearchClient(t)
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, time.Hour)
	stored := integrationNamespace(t, "cheap-model")

	if err := c.Set(context.Background(), stored, "prompt", []byte(`{"answer":"private"}`), 200); err != nil {
		t.Fatalf("Set: %v", err)
	}
	oldVersion := stored
	oldVersion.Version = "v1"

	for name, ns := range map[string]Namespace{
		"tenant":  NewNamespace("different-tenant", stored.Model),
		"model":   NewNamespace("integration-"+t.Name(), "powerful-model"),
		"version": oldVersion,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := c.Get(context.Background(), ns, "prompt")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != nil {
				t.Fatalf("cross-%s lookup returned %+v", name, got)
			}
		})
	}
}

func TestSemanticCacheRejectsDissimilarPrompt(t *testing.T) {
	client := redisSearchClient(t)
	ns := integrationNamespace(t, "cheap-model")
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, time.Hour)
	if err := c.Set(context.Background(), ns, "stored", []byte(`{"answer":"stored"}`), 200); err != nil {
		t.Fatalf("Set: %v", err)
	}

	c.embedder = fixedEmbedder{vector: testVector(1)}
	got, err := c.Get(context.Background(), ns, "unrelated")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("dissimilar prompt returned %+v", got)
	}
}

func TestSemanticCacheEntryExpires(t *testing.T) {
	client := redisSearchClient(t)
	ns := integrationNamespace(t, "cheap-model")
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, 50*time.Millisecond)
	if err := c.Set(context.Background(), ns, "prompt", []byte(`{"answer":"short-lived"}`), 200); err != nil {
		t.Fatalf("Set: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := c.Get(context.Background(), ns, "prompt")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("entry remained searchable after its TTL")
}

func TestSemanticCacheMalformedEntryReturnsError(t *testing.T) {
	client := redisSearchClient(t)
	ns := integrationNamespace(t, "cheap-model")
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, time.Hour)
	key := cacheKey(ns, "prompt")

	if err := client.HSet(context.Background(), key,
		"tenant", ns.Tenant,
		"model", ns.Model,
		"version", ns.Version,
		"created_at", time.Now().Unix(),
		"embedding", float32ToBytes(testVector(0)),
		"data", "{not-json",
	).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if _, err := c.Get(context.Background(), ns, "prompt"); err == nil {
		t.Fatal("Get accepted malformed cached JSON")
	}
}

func TestSemanticCacheIgnoresLegacyUnscopedKeys(t *testing.T) {
	client := redisSearchClient(t)
	ns := integrationNamespace(t, "cheap-model")
	c := newIntegrationCache(t, client, fixedEmbedder{vector: testVector(0)}, time.Hour)
	legacyKey := fmt.Sprintf("cache:%s", hashValue("legacy prompt"))
	t.Cleanup(func() { client.Del(context.Background(), legacyKey) })

	if err := client.HSet(context.Background(), legacyKey,
		"embedding", float32ToBytes(testVector(0)),
		"data", `{"prompt":"legacy","response":"cHJpdmF0ZQ==","status":200}`,
	).Err(); err != nil {
		t.Fatalf("HSet legacy key: %v", err)
	}
	got, err := c.Get(context.Background(), ns, "legacy prompt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("legacy unscoped entry was returned: %+v", got)
	}
}
