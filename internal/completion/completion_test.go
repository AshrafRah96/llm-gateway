package completion

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

// ─────────────────────────── fakes ───────────────────────────

type fakeBody struct {
	io.Reader
	closed bool
}

func (b *fakeBody) Close() error { b.closed = true; return nil }

type fakeProvider struct {
	body   []byte
	sse    string
	status int
	err    error

	completeCalls int
	streamCalls   int
	gotPrompt     string
	gotModel      router.Model
	lastBody      *fakeBody
}

func (p *fakeProvider) Complete(ctx context.Context, prompt string, m router.Model) ([]byte, int, error) {
	p.completeCalls++
	p.gotPrompt, p.gotModel = prompt, m
	if p.err != nil {
		return nil, 0, p.err
	}
	return p.body, p.status, nil
}

func (p *fakeProvider) Stream(ctx context.Context, prompt string, m router.Model) (io.ReadCloser, int, error) {
	p.streamCalls++
	p.gotPrompt, p.gotModel = prompt, m
	if p.err != nil {
		return nil, 0, p.err
	}
	p.lastBody = &fakeBody{Reader: strings.NewReader(p.sse)}
	return p.lastBody, p.status, nil
}

type fakeCache struct {
	entry    *cache.CacheEntry
	beginErr error
	getErr   error
	setErr   error
	getNS    cache.Namespace
	setNS    cache.Namespace
	begins   int
	sets     int
	setBody  []byte
	setStat  int
}

func (c *fakeCache) Begin(ctx context.Context, ns cache.Namespace, prompt string) (cache.Attempt, error) {
	c.begins++
	c.getNS = ns
	if c.beginErr != nil {
		return nil, c.beginErr
	}
	return fakeCacheAttempt{cache: c}, nil
}

type fakeCacheAttempt struct {
	cache *fakeCache
}

func (a fakeCacheAttempt) Get(ctx context.Context) (*cache.CacheEntry, error) {
	c := a.cache
	return c.entry, c.getErr
}

func (a fakeCacheAttempt) Set(ctx context.Context, response []byte, status int) error {
	c := a.cache
	c.sets++
	c.setNS = c.getNS
	c.setBody, c.setStat = response, status
	return c.setErr
}

type fakeRecorder struct {
	calls     int
	key       string
	in, out   int
	cost      float64
	estimated bool
	err       error
}

func (r *fakeRecorder) Record(ctx context.Context, e usage.Entry) error {
	r.calls++
	r.key, r.in, r.out = e.APIKey, e.TokensIn, e.TokensOut
	r.cost, r.estimated = e.CostUSD, e.Estimated
	return r.err
}

func newFixture(p *fakeProvider) (*Completion, *fakeCache, *fakeRecorder) {
	c, r := &fakeCache{}, &fakeRecorder{}
	return New(p, c, r), c, r
}

const okBody = `{"choices":[{"message":{"content":"Paris"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`

// ─────────────────────────── Complete ───────────────────────────

func TestComplete_CacheHitSkipsEverything(t *testing.T) {
	p := &fakeProvider{}
	c, fc, fr := newFixture(p)
	fc.entry = &cache.CacheEntry{Response: []byte(okBody), Status: http.StatusOK}

	got, err := c.Complete(context.Background(), Request{APIKey: "k", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !got.CacheHit {
		t.Error("CacheHit should be true")
	}
	if string(got.Body) != okBody || got.Status != http.StatusOK {
		t.Errorf("got %d %s", got.Status, got.Body)
	}
	if p.completeCalls != 0 {
		t.Error("provider must not be called on a cache hit")
	}
	if fc.sets != 0 {
		t.Error("a cache hit must not rewrite the cache")
	}
	if fr.calls != 0 {
		t.Error("a cache hit must not record usage")
	}
}

func TestComplete_CacheMissCallsRecordsAndStores(t *testing.T) {
	p := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
	c, fc, fr := newFixture(p)

	got, err := c.Complete(context.Background(), Request{APIKey: "key-1", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.CacheHit {
		t.Error("CacheHit should be false")
	}
	if got.Model != router.Cheap.ID {
		t.Errorf("Model = %q, want %q", got.Model, router.Cheap.ID)
	}
	if p.completeCalls != 1 || p.gotPrompt != "hi" || p.gotModel != router.Cheap {
		t.Errorf("provider got %d calls, prompt %q, model %+v", p.completeCalls, p.gotPrompt, p.gotModel)
	}

	if fc.sets != 1 || string(fc.setBody) != okBody || fc.setStat != http.StatusOK {
		t.Errorf("cache stored %d times: %d %s", fc.sets, fc.setStat, fc.setBody)
	}
	if fc.begins != 1 {
		t.Errorf("cache began %d attempts, want one lookup/store attempt", fc.begins)
	}
	if fc.getNS != fc.setNS {
		t.Errorf("cache read namespace %+v differs from write namespace %+v", fc.getNS, fc.setNS)
	}
	if fc.getNS.Model != router.Cheap.ID || fc.getNS.Version == "" {
		t.Errorf("cache namespace = %+v, want cheap model and a schema version", fc.getNS)
	}
	if fc.getNS.Tenant == "" || fc.getNS.Tenant == "key-1" {
		t.Errorf("tenant = %q, want a non-empty fingerprint rather than the raw API key", fc.getNS.Tenant)
	}

	if fr.calls != 1 {
		t.Fatalf("usage recorded %d times, want 1", fr.calls)
	}
	if fr.key != "key-1" || fr.in != 10 || fr.out != 20 {
		t.Errorf("recorded key=%q in=%d out=%d", fr.key, fr.in, fr.out)
	}
	if want := router.Cheap.Cost(10, 20); fr.cost != want {
		t.Errorf("cost = %v, want %v", fr.cost, want)
	}
}

func TestComplete_RoutesBeforeCacheLookup(t *testing.T) {
	p := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
	c, fc, _ := newFixture(p)

	_, err := c.Complete(context.Background(), Request{
		APIKey: "tenant-key",
		Prompt: "analyze this dataset",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.getNS.Model != router.Powerful.ID {
		t.Errorf("cache lookup model = %q, want %q", fc.getNS.Model, router.Powerful.ID)
	}
}

func TestComplete_CacheReadErrorFallsThrough(t *testing.T) {
	p := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
	c, fc, _ := newFixture(p)
	fc.getErr = errors.New("redis down")

	got, err := c.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("a cache read failure must not fail the request: %v", err)
	}
	if got.Status != http.StatusOK || p.completeCalls != 1 {
		t.Errorf("status %d, provider calls %d", got.Status, p.completeCalls)
	}
}

func TestComplete_ProviderErrorRecordsNothing(t *testing.T) {
	p := &fakeProvider{err: errors.New("dial tcp: refused")}
	c, fc, fr := newFixture(p)

	if _, err := c.Complete(context.Background(), Request{Prompt: "hi"}); err == nil {
		t.Fatal("expected an error")
	}
	if fc.sets != 0 || fr.calls != 0 {
		t.Errorf("failed call stored %d and recorded %d", fc.sets, fr.calls)
	}
}

func TestComplete_NonOKIsNotCachedButIsCounted(t *testing.T) {
	p := &fakeProvider{body: []byte(`{"error":"nope"}`), status: http.StatusTooManyRequests}
	c, fc, fr := newFixture(p)

	got, err := c.Complete(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", got.Status)
	}
	if fc.sets != 0 {
		t.Error("a failed upstream response must not be cached")
	}
	// requests counts attempts; ParseTokens yields 0/0 so the cost is 0.
	if fr.calls != 1 || fr.cost != 0 {
		t.Errorf("recorded %d times at cost %v", fr.calls, fr.cost)
	}
}

func TestComplete_RoutesOnPrompt(t *testing.T) {
	for _, tt := range []struct {
		prompt string
		want   router.Model
	}{
		{"hi", router.Cheap},
		{"analyze this dataset", router.Powerful},
	} {
		p := &fakeProvider{body: []byte(okBody), status: http.StatusOK}
		c, _, _ := newFixture(p)

		got, err := c.Complete(context.Background(), Request{Prompt: tt.prompt})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.gotModel != tt.want || got.Model != tt.want.ID {
			t.Errorf("Route(%q) reached provider as %+v, want %+v", tt.prompt, p.gotModel, tt.want)
		}
	}
}
