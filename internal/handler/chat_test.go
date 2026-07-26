package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

// The handler tests drive a real completion module over fake collaborators, so they
// exercise everything except the network.

type fakeProvider struct {
	body   []byte
	sse    string
	status int
	err    error
}

func (p *fakeProvider) Complete(ctx context.Context, prompt string, m router.Model) ([]byte, int, error) {
	if p.err != nil {
		return nil, 0, p.err
	}
	return p.body, p.status, nil
}

func (p *fakeProvider) Stream(ctx context.Context, prompt string, m router.Model) (io.ReadCloser, int, error) {
	if p.err != nil {
		return nil, 0, p.err
	}
	return io.NopCloser(strings.NewReader(p.sse)), p.status, nil
}

type fakeCache struct{ entry *cache.CacheEntry }

func (c *fakeCache) Get(ctx context.Context, prompt string) (*cache.CacheEntry, error) {
	return c.entry, nil
}
func (c *fakeCache) Set(ctx context.Context, prompt string, response []byte, status int) error {
	return nil
}

type fakeRecorder struct{}

func (fakeRecorder) Record(ctx context.Context, e usage.Entry) error { return nil }

const upstreamBody = `{"choices":[{"message":{"content":"Paris"}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`

func newTestServer(p *fakeProvider, c *fakeCache) http.Handler {
	if c == nil {
		c = &fakeCache{}
	}
	return NewServer(New(completion.New(p, c, fakeRecorder{}), nil, nil))
}

func post(t *testing.T, srv http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestChat_InvalidBody(t *testing.T) {
	w := post(t, newTestServer(&fakeProvider{}, nil), "/chat", "not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChat_MissingPrompt(t *testing.T) {
	w := post(t, newTestServer(&fakeProvider{}, nil), "/chat", `{"prompt": ""}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChat_UpstreamError(t *testing.T) {
	p := &fakeProvider{err: errors.New("dial tcp: connection refused")}
	w := post(t, newTestServer(p, nil), "/chat", `{"prompt": "hello"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestChat_ProxiesResponse(t *testing.T) {
	p := &fakeProvider{body: []byte(upstreamBody), status: http.StatusOK}
	w := post(t, newTestServer(p, nil), "/chat", `{"prompt": "What is the capital of France?"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %s", ct)
	}
	if got := w.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache = %q, want MISS", got)
	}
	if got := w.Header().Get("X-Model"); got != router.Cheap.ID {
		t.Errorf("X-Model = %q, want %q", got, router.Cheap.ID)
	}
	if w.Body.String() != upstreamBody {
		t.Errorf("body = %s", w.Body.String())
	}
}

// This path had no test before: the handler could only be built with a nil cache.
func TestChat_CacheHit(t *testing.T) {
	p := &fakeProvider{err: errors.New("provider must not be called")}
	c := &fakeCache{entry: &cache.CacheEntry{Response: []byte(upstreamBody), Status: http.StatusOK}}

	w := post(t, newTestServer(p, c), "/chat", `{"prompt": "France's capital?"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", got)
	}
	if w.Body.String() != upstreamBody {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestChatStream_ProxiesSSE(t *testing.T) {
	p := &fakeProvider{
		status: http.StatusOK,
		sse: "data: {\"choices\":[{\"delta\":{\"content\":\"Par\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"is\"}}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n" +
			"data: [DONE]\n\n",
	}

	w := post(t, newTestServer(p, nil), "/chat/stream", `{"prompt": "hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %s", ct)
	}
	if got := w.Header().Get("X-Model"); got != router.Cheap.ID {
		t.Errorf("X-Model = %q, want %q", got, router.Cheap.ID)
	}

	body := w.Body.String()
	if !strings.Contains(body, `"content":"Par"`) || !strings.Contains(body, `"content":"is"`) {
		t.Errorf("chunks missing from response: %s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Errorf("stream did not terminate with [DONE]: %s", body)
	}
	// The usage chunk is ours, for billing. It must not reach the client, whose stream
	// should look exactly as it did before we started asking for it.
	if strings.Contains(body, "prompt_tokens") {
		t.Errorf("billing chunk leaked into the client stream: %s", body)
	}
}

func TestChatStream_InvalidBody(t *testing.T) {
	w := post(t, newTestServer(&fakeProvider{}, nil), "/chat/stream", "not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChatStream_UpstreamError(t *testing.T) {
	p := &fakeProvider{err: errors.New("dial tcp: connection refused")}
	w := post(t, newTestServer(p, nil), "/chat/stream", `{"prompt": "hello"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

// Both entry points must report the same upstream response the same way. Flattening a
// 429 to 502 here would break client backoff and put the two endpoints back out of sync.
func TestChatEndpointsAgreeOnUpstreamStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusServiceUnavailable,
		http.StatusUnauthorized,
	} {
		body := &fakeProvider{body: []byte(`{"error":"nope"}`), status: status}
		chat := post(t, newTestServer(body, nil), "/chat", `{"prompt": "hello"}`)
		if chat.Code != status {
			t.Errorf("/chat returned %d for upstream %d", chat.Code, status)
		}

		sse := &fakeProvider{sse: `{"error":"nope"}`, status: status}
		stream := post(t, newTestServer(sse, nil), "/chat/stream", `{"prompt": "hello"}`)
		if stream.Code != status {
			t.Errorf("/chat/stream returned %d for upstream %d", stream.Code, status)
		}
	}
}

func TestHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	newTestServer(&fakeProvider{}, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestModels(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	w := httptest.NewRecorder()
	newTestServer(&fakeProvider{}, nil).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got []ModelInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// /models must be the catalogue, not a second copy of it.
	want := router.All()
	if len(got) != len(want) {
		t.Fatalf("served %d models, catalogue has %d", len(got), len(want))
	}
	for i, m := range want {
		if got[i].ID != m.ID || got[i].Description != m.Description {
			t.Errorf("model %d = %+v, catalogue has %+v", i, got[i], m)
		}
		if got[i].CostPer1KIn != m.PriceIn || got[i].CostPer1KOut != m.PriceOut {
			t.Errorf("%s priced %v/%v, catalogue says %v/%v",
				m.ID, got[i].CostPer1KIn, got[i].CostPer1KOut, m.PriceIn, m.PriceOut)
		}
	}
}
