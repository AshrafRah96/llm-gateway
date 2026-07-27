package cache

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fixedEmbedder struct {
	vector []float32
	err    error
}

func (e fixedEmbedder) Embed(context.Context, string) ([]float32, error) {
	return e.vector, e.err
}

func testVector(axis int) []float32 {
	vector := make([]float32, vectorDim)
	vector[axis] = 1
	return vector
}

func TestNewNamespaceFingerprintsTenantAndIncludesModelAndVersion(t *testing.T) {
	ns := NewNamespace("raw-secret-key", "model-a")

	if ns.Tenant == "" || ns.Tenant == "raw-secret-key" {
		t.Fatalf("Tenant = %q, want a one-way fingerprint", ns.Tenant)
	}
	if strings.Contains(ns.Tenant, "raw-secret-key") {
		t.Fatalf("Tenant %q contains the raw key", ns.Tenant)
	}
	if ns.Model != "model-a" || ns.Version != SchemaVersion {
		t.Fatalf("namespace = %+v", ns)
	}
	if ns != NewNamespace("raw-secret-key", "model-a") {
		t.Fatal("the same key and model must produce a stable namespace")
	}
	if ns.Tenant == NewNamespace("another-key", "model-a").Tenant {
		t.Fatal("different API keys must not share a tenant fingerprint")
	}
}

func TestCacheKeyContainsNoRawAPIKey(t *testing.T) {
	ns := NewNamespace("raw-secret-key", "model-a")
	key := cacheKey(ns, "private prompt")

	if !strings.HasPrefix(key, "cache:v2:") {
		t.Fatalf("key = %q, want the versioned prefix", key)
	}
	for _, secret := range []string{"raw-secret-key", "private prompt"} {
		if strings.Contains(key, secret) {
			t.Fatalf("key %q contains %q", key, secret)
		}
	}
}

func TestEscapeTagValueProtectsRedisSearchSyntax(t *testing.T) {
	if got := escapeTagValue(`gpt-4.1 mini`); got != `gpt-4.1 mini` {
		t.Fatalf("ordinary model name = %q", got)
	}
	if got := escapeTagValue(`cost${x}|\usd`); got != `cost\$\{x\}\|\\usd` {
		t.Fatalf("query syntax characters = %q", got)
	}
}

func TestParseTTL(t *testing.T) {
	got, err := ParseTTL("")
	if err != nil || got != DefaultTTL {
		t.Fatalf("ParseTTL(empty) = %v, %v; want %v", got, err, DefaultTTL)
	}

	got, err = ParseTTL("90m")
	if err != nil || got != 90*time.Minute {
		t.Fatalf("ParseTTL(90m) = %v, %v", got, err)
	}

	for _, value := range []string{"not-a-duration", "0s", "-1m"} {
		if _, err := ParseTTL(value); err == nil {
			t.Errorf("ParseTTL(%q) succeeded, want an error", value)
		}
	}
}

func TestSemanticCacheRejectsUnexpectedEmbeddingDimensions(t *testing.T) {
	c := &SemanticCache{embedder: fixedEmbedder{vector: []float32{1}}, ttl: time.Hour}

	if _, err := c.Get(context.Background(), NewNamespace("key", "model"), "prompt"); err == nil {
		t.Fatal("Get accepted an embedding with the wrong dimensions")
	}
	if err := c.Set(context.Background(), NewNamespace("key", "model"), "prompt", []byte("{}"), 200); err == nil {
		t.Fatal("Set accepted an embedding with the wrong dimensions")
	}
}

func TestSemanticCacheSearchFailureIsReturned(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:       "127.0.0.1:0",
		MaxRetries: -1,
	})
	t.Cleanup(func() { client.Close() })

	c := &SemanticCache{
		client:   client,
		embedder: fixedEmbedder{vector: testVector(0)},
		ttl:      time.Hour,
	}
	if _, err := c.Get(context.Background(), NewNamespace("key", "model"), "prompt"); err == nil {
		t.Fatal("Get hid the Redis search failure")
	}
}

func TestParseSearchResultRESP3(t *testing.T) {
	c := &SemanticCache{}
	results := map[interface{}]interface{}{
		"total_results": int64(1),
		"results": []interface{}{
			map[interface{}]interface{}{
				"id": "cache:v2:key",
				"extra_attributes": map[interface{}]interface{}{
					"data":  `{"prompt":"p","response":"UGFyaXM=","status":200}`,
					"score": "0.01",
				},
			},
		},
	}

	entry, err := c.parseSearchResult(results)
	if err != nil {
		t.Fatalf("parseSearchResult: %v", err)
	}
	if entry == nil || string(entry.Response) != "Paris" || entry.Status != 200 {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestParseSearchResultRESP3RejectsMalformedData(t *testing.T) {
	c := &SemanticCache{}
	results := map[interface{}]interface{}{
		"total_results": int64(1),
		"results": []interface{}{
			map[interface{}]interface{}{
				"extra_attributes": map[interface{}]interface{}{
					"data":  "{not-json",
					"score": "0",
				},
			},
		},
	}

	if _, err := c.parseSearchResult(results); err == nil {
		t.Fatal("parseSearchResult accepted malformed RESP3 data")
	}
}

func TestParseSearchResultRESP3TreatsExpiredDocumentAsMiss(t *testing.T) {
	c := &SemanticCache{}
	results := map[interface{}]interface{}{
		"total_results": int64(1),
		"results": []interface{}{
			map[interface{}]interface{}{
				"id":               "cache:v2:expired",
				"extra_attributes": map[interface{}]interface{}{},
			},
		},
	}

	entry, err := c.parseSearchResult(results)
	if err != nil {
		t.Fatalf("parseSearchResult: %v", err)
	}
	if entry != nil {
		t.Fatalf("entry = %+v, want a miss", entry)
	}
}
