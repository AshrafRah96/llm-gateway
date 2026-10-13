package completion

import (
	"context"
	"crypto/rand"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/redis/go-redis/v9"
)

type policyEmbedder struct{}

func (policyEmbedder) Embed(context.Context, string) ([]float32, error) {
	vector := make([]float32, cache.EmbeddingDimension)
	vector[0] = 1
	return vector, nil
}

func TestPolicyCacheIsolationWithRedis(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2})
	t.Cleanup(func() { client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis is not reachable: %v", err)
	}
	if _, err := client.Do(ctx, "FT._LIST").Result(); err != nil {
		t.Skipf("Redis Search is not available: %v", err)
	}
	store, err := cache.NewSemanticCache(ctx, client, policyEmbedder{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, streaming := range []bool{false, true} {
		project := "policy-" + rand.Text()
		req := Request{ProjectID: project, Prompt: "hello"}
		old, err := store.Begin(ctx, cache.NewProjectNamespace(project, router.Cheap.ID, "42"), req.Prompt)
		if err != nil {
			t.Fatal(err)
		}
		if err := old.Set(ctx, []byte(okBody), http.StatusOK); err != nil {
			t.Fatal(err)
		}
		p := &fakeProvider{body: []byte(okBody), status: http.StatusOK, events: completeEvents}
		c := NewWithRouting(p, store, &fakeRecorder{}, router.Default(), fakeRetriever{result: Retrieval{CorpusVersion: "42"}})
		// The first call must reject the real legacy entry; the second must reuse the
		// newly written entry under the policy namespace in the same Redis index.
		for _, wantHit := range []bool{false, true} {
			var hit bool
			if streaming {
				s, err := c.Stream(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				hit = s.CacheHit()
				drain(t, s)
				s.Close()
			} else {
				resp, err := c.Complete(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				hit = resp.CacheHit
			}
			if hit != wantHit {
				t.Fatalf("streaming=%v cache hit=%v, want %v", streaming, hit, wantHit)
			}
		}
		if p.completeCalls+p.streamCalls != 1 {
			t.Fatalf("provider called %d times, want 1", p.completeCalls+p.streamCalls)
		}
	}
}
