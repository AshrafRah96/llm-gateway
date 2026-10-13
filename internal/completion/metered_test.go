package completion

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	cachepkg "github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
)

func meteredLedger(t *testing.T) (*usage.Tracker, *redis.Client, string, string) {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Protocol: 2, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		t.Skipf("no test Redis at %s: %v", addr, err)
	}
	project := "metered-test:" + rand.Text()
	prefix := fmt.Sprintf("ledger:{%x}:", sha256.Sum256([]byte(project)))
	t.Cleanup(func() {
		cleanup := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
		defer cleanup.Close()
		iter := cleanup.Scan(context.Background(), 0, prefix+"*", 100).Iterator()
		for iter.Next(context.Background()) {
			cleanup.Del(context.Background(), iter.Val())
		}
		client.Close()
	})
	return usage.NewTracker(client), client, project, prefix
}

type identifiedProvider struct {
	Provider
	projects []string
	ids      []string
	closeErr error
}

func (p *identifiedProvider) identify(ctx context.Context) {
	p.projects = append(p.projects, usage.Project(ctx))
	p.ids = append(p.ids, usage.ClientRequestID(ctx))
	usage.ProviderRequestID(ctx, "fake-provider-"+usage.ClientRequestID(ctx))
}

func (p *identifiedProvider) Complete(ctx context.Context, input ProviderInput, m router.Model) ([]byte, int, error) {
	p.identify(ctx)
	return p.Provider.Complete(ctx, input, m)
}

func (p *identifiedProvider) Stream(ctx context.Context, input ProviderInput, m router.Model) (ProviderStream, int, error) {
	p.identify(ctx)
	body, status, err := p.Provider.Stream(ctx, input, m)
	if body != nil && p.closeErr != nil {
		body = &closeErrorStream{ProviderStream: body, err: p.closeErr}
	}
	return body, status, err
}

func meteredState(t *testing.T, ledger *usage.Tracker, project string, wantPending int, wantRequests int64) *usage.Stats {
	t.Helper()
	pending, err := ledger.Pending(context.Background(), project)
	if err != nil || len(pending) != wantPending {
		t.Fatalf("pending=%+v, err=%v; want %d", pending, err, wantPending)
	}
	stats, err := ledger.Get(context.Background(), project)
	if err != nil || stats.Requests != wantRequests {
		t.Fatalf("stats=%+v, err=%v; want %d requests", stats, err, wantRequests)
	}
	audit, err := ledger.AuditTotals(context.Background(), project, false)
	if err != nil || !audit.Match {
		t.Fatalf("audit=%+v, err=%v", audit, err)
	}
	return stats
}

func TestMeteredJSONSettlesKnownUsageAndCacheReplayIsFree(t *testing.T) {
	ledger, client, project, prefix := meteredLedger(t)
	fake := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
	provider := &identifiedProvider{Provider: fake}
	cache := &fakeCache{}
	c := NewWithLedger(provider, cache, ledger, router.Default(), nil)
	req := Request{ProjectID: project, Prompt: "hello"}
	response, err := c.Complete(context.Background(), req)
	if err != nil || response.Status != http.StatusOK || cache.sets != 1 {
		t.Fatalf("response=%+v, err=%v, cache stores=%d", response, err, cache.sets)
	}
	stats := meteredState(t, ledger, project, 0, 1)
	if stats.TokensIn != 10 || stats.TokensOut != 20 || stats.CostUSD != router.Cheap.Cost(10, 20) || stats.EstimatedRequests != 0 {
		t.Fatalf("wrong measured charge: %+v", stats)
	}
	if len(provider.ids) != 1 || provider.ids[0] == "" || provider.projects[0] != project {
		t.Fatalf("operation identity lost: %+v", provider)
	}
	raw, err := client.HGet(context.Background(), prefix+"events", provider.ids[0]).Result()
	var event usage.Entry
	if err != nil || json.Unmarshal([]byte(raw), &event) != nil || event.ProviderRequestID != "fake-provider-"+provider.ids[0] || event.State != "settled" {
		t.Fatalf("invalid identified event: %s, %v", raw, err)
	}
	cache.entry = &cachepkg.CacheEntry{Response: []byte(okBody), Status: http.StatusOK}
	response, err = c.Complete(context.Background(), req)
	if err != nil || !response.CacheHit || fake.completeCalls != 1 {
		t.Fatalf("replay=%+v, err=%v, provider calls=%d", response, err, fake.completeCalls)
	}
	meteredState(t, ledger, project, 0, 1)
}

func TestMeteredJSONInvalidUsageRetainsHoldAndRejects(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"content":"answer"}}]}`,
		`{"usage":{"prompt_tokens":10}}`,
		`{"usage":{"prompt_tokens":-1,"completion_tokens":20}}`,
		`{"usage":{"prompt_tokens":10,"completion_tokens":513}}`,
	} {
		t.Run(body, func(t *testing.T) {
			ledger, _, project, _ := meteredLedger(t)
			provider := &fakeProvider{body: []byte(body), status: http.StatusOK}
			cache := &fakeCache{}
			c := NewWithLedger(provider, cache, ledger, router.Default(), nil)
			if _, err := c.Complete(context.Background(), Request{ProjectID: project, Prompt: "hello"}); err == nil {
				t.Fatal("missing/invalid usage accepted")
			}
			stats := meteredState(t, ledger, project, 1, 1)
			if provider.completeCalls != 1 || cache.sets != 0 || stats.EstimatedRequests != 1 || stats.TokensIn <= 0 {
				t.Fatalf("calls=%d, stores=%d, stats=%+v", provider.completeCalls, cache.sets, stats)
			}
		})
	}
}

func TestMeteredFallbackChargesDistinctAttempts(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			ledger, client, project, prefix := meteredLedger(t)
			fake := &sequencedProvider{results: []struct {
				body   []byte
				status int
				err    error
			}{{status: http.StatusServiceUnavailable}, {body: []byte(okBody), status: http.StatusOK}}}
			provider := &identifiedProvider{Provider: fake}
			r := router.Default()
			r.CheapFallback = router.Powerful
			cache := &fakeCache{}
			c := NewWithLedger(provider, cache, ledger, r, nil)
			req := Request{ProjectID: project, Prompt: "hello"}
			if streaming {
				s, err := c.Stream(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				chunks := drain(t, s)
				if !s.Fallback() || s.Model() != router.Powerful.ID || len(chunks) != 3 || !chunks[2].Done || s.Close() != nil {
					t.Fatalf("fallback stream: %+v", chunks)
				}
			} else {
				response, err := c.Complete(context.Background(), req)
				if err != nil || !response.Fallback || response.Model != router.Powerful.ID {
					t.Fatalf("fallback response=%+v, %v", response, err)
				}
			}
			stats := meteredState(t, ledger, project, 1, 2)
			if len(provider.ids) != 2 || provider.ids[0] == provider.ids[1] || provider.projects[0] != project || provider.projects[1] != project || cache.sets != 1 {
				t.Fatalf("identities=%+v, stores=%d", provider, cache.sets)
			}
			primaryCost, _ := router.Cheap.CostNanos((ProviderInput{Question: req.Prompt}).estimateTokens(), 0)
			fallbackCost, _ := router.Powerful.CostNanos(10, 20)
			want := money.USD(primaryCost + fallbackCost)
			if stats.CostUSD != want || stats.EstimatedRequests != 1 {
				t.Fatalf("fallback charge=%+v, want cost %v", stats, want)
			}
			for _, id := range provider.ids {
				raw := client.HGet(context.Background(), prefix+"events", id).Val()
				if raw == "" {
					t.Fatalf("missing attempt %s", id)
				}
			}
		})
	}
}

func TestMeteredAbandonedStreamRetainsHoldAfterCancellation(t *testing.T) {
	ledger, _, project, _ := meteredLedger(t)
	fake := &fakeProvider{events: completeEvents, status: http.StatusOK}
	cache := &fakeCache{}
	c := NewWithLedger(fake, cache, ledger, router.Default(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := c.Stream(ctx, Request{ProjectID: project, Prompt: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if chunk, ok := s.Next(); !ok || chunk.Content != "Par" {
		t.Fatal("missing initial content")
	}
	cancel()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	stats := meteredState(t, ledger, project, 1, 1)
	if stats.EstimatedRequests != 1 || stats.TokensOut == 0 || cache.sets != 0 || !fake.lastStream.closed {
		t.Fatalf("abandoned accounting=%+v, stores=%d", stats, cache.sets)
	}
}

func TestMeteredAdmissionRejectsWithoutCallingProvider(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("exhausted=%v/stream=%v", exhausted, streaming), func(t *testing.T) {
				ledger, client, project, _ := meteredLedger(t)
				if exhausted {
					if err := ledger.SetLimits(context.Background(), project, 1, 0); err != nil {
						t.Fatal(err)
					}
				} else {
					client.Close()
				}
				fake := &fakeProvider{body: []byte(okBody), events: completeEvents, status: http.StatusOK}
				r := router.Default()
				r.CheapFallback = router.Powerful
				c := NewWithLedger(fake, &fakeCache{}, ledger, r, nil)
				req := Request{ProjectID: project, Prompt: "hello"}
				var err error
				if streaming {
					_, err = c.Stream(context.Background(), req)
				} else {
					_, err = c.Complete(context.Background(), req)
				}
				var budget *usage.BudgetError
				if !errors.As(err, &budget) || budget.Exhausted != exhausted || fake.completeCalls != 0 || fake.streamCalls != 0 {
					t.Fatalf("admission=%v, provider calls=%d/%d", err, fake.completeCalls, fake.streamCalls)
				}
			})
		}
	}
}

func TestMeteredStreamClosePreservesTransportAndLedgerErrors(t *testing.T) {
	for _, mode := range []string{"known close failure", "missing usage close failure", "missing usage ledger failure"} {
		t.Run(mode, func(t *testing.T) {
			ledger, client, project, prefix := meteredLedger(t)
			closeErr := errors.New("transport close failed")
			fake := &fakeProvider{events: completeEvents, status: http.StatusOK}
			if mode != "known close failure" {
				fake.events = []ProviderEvent{{Content: "partial"}, {Done: true}}
			}
			provider := &identifiedProvider{Provider: fake, closeErr: closeErr}
			cache := &fakeCache{}
			c := NewWithLedger(provider, cache, ledger, router.Default(), nil)
			s, err := c.Stream(context.Background(), Request{ProjectID: project, Prompt: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "missing usage ledger failure" {
				if err := client.Set(context.Background(), prefix+"totals", "corrupt", 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			for _, chunk := range drain(t, s) {
				if chunk.Done {
					t.Error("failure emitted successful terminal chunk")
				}
			}
			for i := 0; i < 2; i++ {
				err := s.Close()
				if !errors.Is(err, closeErr) {
					t.Errorf("lost close error: %v", err)
				}
				if mode != "known close failure" && (err == nil || !strings.Contains(err.Error(), "missing or invalid terminal provider usage")) {
					t.Errorf("lost metadata error: %v", err)
				}
				var budget *usage.BudgetError
				if mode == "missing usage ledger failure" && !errors.As(err, &budget) {
					t.Errorf("lost ledger error: %v", err)
				}
			}
			if cache.sets != 0 {
				t.Fatal("failed stream cached")
			}
			pending, err := ledger.Pending(context.Background(), project)
			wantPending := 1
			if mode == "known close failure" {
				wantPending = 0
			}
			if err != nil || len(pending) != wantPending {
				t.Fatalf("pending=%+v, %v", pending, err)
			}
		})
	}
}

func TestMeteredPreparationBudgetErrorsRejectBeforeGeneration(t *testing.T) {
	for _, stage := range []string{"retrieval", "cache"} {
		for _, failure := range []string{"exhausted", "unavailable", "ordinary"} {
			for _, streaming := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", stage, failure, streaming), func(t *testing.T) {
					ledger, _, project, _ := meteredLedger(t)
					var preparationErr error = &usage.BudgetError{Exhausted: failure == "exhausted"}
					if failure == "ordinary" {
						preparationErr = errors.New("temporary lookup failure")
					}
					fake := &fakeProvider{body: []byte(okBody), events: completeEvents, status: http.StatusOK}
					cache := &fakeCache{}
					var retriever Retriever
					if stage == "retrieval" {
						retriever = fakeRetriever{err: fmt.Errorf("embed: %w", preparationErr)}
					} else {
						cache.beginErr = fmt.Errorf("embed: %w", preparationErr)
					}
					r := router.Default()
					r.CheapFallback = router.Powerful
					c := NewWithLedger(fake, cache, ledger, r, retriever)
					req := Request{ProjectID: project, Prompt: "hello"}
					var err error
					var grounded, hit bool
					if streaming {
						var s *Stream
						s, err = c.Stream(context.Background(), req)
						if err == nil {
							grounded, hit = s.Grounded(), s.CacheHit()
							drain(t, s)
							err = s.Close()
						}
					} else {
						var response Response
						response, err = c.Complete(context.Background(), req)
						grounded, hit = response.Grounded, response.CacheHit
					}
					if failure == "ordinary" {
						if err != nil || grounded || hit || fake.completeCalls+fake.streamCalls != 1 {
							t.Fatalf("ordinary lookup error changed fallback: err=%v, grounded=%v, hit=%v, calls=%d/%d", err, grounded, hit, fake.completeCalls, fake.streamCalls)
						}
						meteredState(t, ledger, project, 0, 1)
					} else {
						if !errors.Is(err, preparationErr) || fake.completeCalls+fake.streamCalls != 0 || cache.sets != 0 {
							t.Fatalf("budget error bypassed: err=%v, calls=%d/%d, stores=%d", err, fake.completeCalls, fake.streamCalls, cache.sets)
						}
						if stage == "retrieval" && cache.begins != 0 {
							t.Fatal("cache preparation ran after retrieval budget failure")
						}
						meteredState(t, ledger, project, 0, 0)
					}
				})
			}
		}
	}
}

type localEmbeddingTransport func(*http.Request) (*http.Response, error)

func (f localEmbeddingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMeteredSemanticCacheHitStillChargesEmbedding(t *testing.T) {
	ledger, client, project, _ := meteredLedger(t)
	vector := make([]float32, cachepkg.EmbeddingDimension)
	vector[0] = 1
	body, err := json.Marshal(map[string]any{"data": []map[string]any{{"embedding": vector}}, "usage": map[string]int{"prompt_tokens": 2}})
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultClient
	embeddingCalls := 0
	http.DefaultClient = &http.Client{Transport: localEmbeddingTransport(func(r *http.Request) (*http.Response, error) {
		embeddingCalls++
		if r.URL.Host != "api.openai.com" || r.URL.Path != "/v1/embeddings" || r.Header.Get("X-Client-Request-Id") == "" {
			t.Errorf("unexpected embedding request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = previous })
	semantic, err := cachepkg.NewSemanticCache(context.Background(), client, cachepkg.NewMeteredEmbeddingClient("fake-key", ledger, "cache_lookup"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ns := cachepkg.NewProjectNamespace(project, router.Cheap.ID, "")
	ns.Version += ":" + promptPolicyVersion
	cachePrefix := fmt.Sprintf("cache:v2:%x:", sha256.Sum256([]byte(ns.Tenant+"\x00"+ns.Model+"\x00"+ns.Version)))
	t.Cleanup(func() {
		iter := client.Scan(context.Background(), 0, cachePrefix+"*", 100).Iterator()
		for iter.Next(context.Background()) {
			client.Del(context.Background(), iter.Val())
		}
	})
	fake := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
	c := NewWithLedger(fake, semantic, ledger, router.Default(), nil)
	req := Request{ProjectID: project, Prompt: "hello"}
	if response, err := c.Complete(context.Background(), req); err != nil || response.CacheHit {
		t.Fatalf("initial response=%+v, %v", response, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		result, err := client.FTSearch(context.Background(), "prompt_cache_v2", "@tenant:{"+ns.Tenant+"}").Result()
		if err != nil {
			t.Fatal(err)
		}
		if result.Total > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache entry never indexed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := c.Complete(context.Background(), req)
	if err != nil || !response.CacheHit || fake.completeCalls != 1 || embeddingCalls != 2 {
		t.Fatalf("replay=%+v, %v; calls chat=%d embedding=%d", response, err, fake.completeCalls, embeddingCalls)
	}
	stats := meteredState(t, ledger, project, 0, 1)
	if stats.EmbeddingRequests != 2 || stats.EmbeddingTokens != 4 || stats.EmbeddingCostUSD != money.USD(80) {
		t.Fatalf("cache-hit embeddings not charged: %+v", stats)
	}
}
