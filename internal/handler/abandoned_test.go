package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

type capturingRecorder struct {
	mu      sync.Mutex
	entries []usage.Entry
}

func (r *capturingRecorder) Record(ctx context.Context, e usage.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *capturingRecorder) only(t *testing.T) usage.Entry {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) != 1 {
		t.Fatalf("recorded %d entries, want 1", len(r.entries))
	}
	return r.entries[0]
}

// slowUpstream streams chunks with a gap between them and only reports usage at the
// very end, exactly as OpenAI does with stream_options.include_usage.
func slowUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		for i := range 10 {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chunk-%d \"}}]}\n\n", i)
			f.Flush()
			select {
			case <-r.Context().Done():
				return // the gateway hung up: stop generating, and never report usage
			case <-time.After(25 * time.Millisecond):
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func streamServer(upstreamURL string, rec completion.Recorder) http.Handler {
	c := completion.New(
		&provider.OpenAIClient{APIKey: "test-key", APIURL: upstreamURL},
		&fakeCache{},
		rec,
	)
	return NewServer(New(c, nil, nil))
}

// The scenario this whole mechanism exists for: a client hits stop partway through a
// stream. Cancelling kills the upstream read before the usage chunk arrives, so the
// tokens can never be known exactly — but they were consumed, and recording zero would
// quietly write off a real cost.
func TestChatStream_AbandonedByClientIsStillBilled(t *testing.T) {
	upstream := slowUpstream(t)
	rec := &capturingRecorder{}
	srv := streamServer(upstream.URL, rec)

	const prompt = "Write me a long essay about the history of France"

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/chat/stream",
		strings.NewReader(`{"prompt":"`+prompt+`"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "key-1")

	// Hang up after a couple of chunks have gone out.
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()

	srv.ServeHTTP(httptest.NewRecorder(), req)

	got := rec.only(t)
	if !got.Estimated {
		t.Error("charge is not marked estimated, so it reads as a measurement")
	}
	if got.APIKey != "key-1" {
		t.Errorf("billed %q, want key-1", got.APIKey)
	}
	if got.TokensIn == 0 {
		t.Error("prompt tokens billed as zero; the provider charges the full prompt regardless")
	}
	if got.TokensOut == 0 {
		t.Error("completion tokens billed as zero; tokens were generated before the cut")
	}
	if got.CostUSD <= 0 {
		t.Errorf("cost = %v; an abandoned stream is not free", got.CostUSD)
	}
}

// The same path when nobody hangs up: real numbers, and no estimated flag.
func TestChatStream_CompletedIsBilledExactly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	defer upstream.Close()

	rec := &capturingRecorder{}
	srv := streamServer(upstream.URL, rec)

	w := post(t, srv, "/chat/stream", `{"prompt":"What is the capital of France?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	got := rec.only(t)
	if got.Estimated {
		t.Error("a stream that reported usage must not be marked estimated")
	}
	if got.TokensIn != 10 || got.TokensOut != 20 {
		t.Errorf("billed in=%d out=%d, want the reported 10/20", got.TokensIn, got.TokensOut)
	}
}
