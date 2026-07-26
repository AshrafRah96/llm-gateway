package completion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/router"
)

const sseBody = `data: {"choices":[{"delta":{"content":"Par"}}]}

data: {"choices":[{"delta":{"content":"is"}}]}

data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20}}

data: [DONE]

`

func drain(t *testing.T, s *Stream) []string {
	t.Helper()
	var out []string
	for data, ok := s.Next(); ok; data, ok = s.Next() {
		out = append(out, data)
	}
	return out
}

func TestStream_PassesChunksThroughInOrder(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, _, _ := newFixture(p)

	s, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	got := drain(t, s)
	if len(got) != 3 {
		t.Fatalf("got %d chunks: %q", len(got), got)
	}
	if got[0] != `{"choices":[{"delta":{"content":"Par"}}]}` {
		t.Errorf("first chunk = %s", got[0])
	}
	if got[len(got)-1] != "[DONE]" {
		t.Errorf("last chunk = %q, want [DONE]", got[len(got)-1])
	}
	if s.Model() != router.Cheap.ID {
		t.Errorf("Model() = %q, want %q", s.Model(), router.Cheap.ID)
	}
}

// We ask OpenAI for stream_options.include_usage so we can bill the stream. The client
// did not ask for it, and never saw that chunk before we started requesting it. Its
// choices array is empty, so forwarding it breaks any client that reads choices[0]
// without checking the length.
func TestStream_UsageChunkIsNotForwardedToTheClient(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, _, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "k", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, data := range drain(t, s) {
		if data == "[DONE]" {
			continue
		}
		var chunk sseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %v (%s)", err, data)
		}
		if len(chunk.Choices) == 0 {
			t.Errorf("forwarded a chunk with no choices: %s", data)
		}
		if chunk.Usage != nil {
			t.Errorf("forwarded our billing chunk to the client: %s", data)
		}
	}

	// Swallowed, not ignored: the tokens still have to reach the meter.
	s.Close()
	if fr.in != 10 || fr.out != 20 {
		t.Errorf("recorded in=%d out=%d, want 10/20", fr.in, fr.out)
	}
}

// This is the regression the review found: streams used to be unmetered entirely.
func TestStream_CloseRecordsUsage(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, _, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "key-1", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drain(t, s)

	if fr.calls != 0 {
		t.Error("usage should be recorded on Close, not mid-stream")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
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
	if !p.lastBody.closed {
		t.Error("upstream body was not closed")
	}
}

// A streamed answer must land in the cache in the same shape a /chat answer does,
// so either entry point can serve the other's traffic.
func TestStream_CloseCachesAssembledCompletion(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, fc, _ := newFixture(p)

	s, _ := c.Stream(context.Background(), Request{Prompt: "hi"})
	drain(t, s)
	s.Close()

	if fc.sets != 1 || fc.setStat != http.StatusOK {
		t.Fatalf("cached %d times with status %d", fc.sets, fc.setStat)
	}

	var stored struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(fc.setBody, &stored); err != nil {
		t.Fatalf("stored body is not a completion: %v (%s)", err, fc.setBody)
	}
	if len(stored.Choices) != 1 || stored.Choices[0].Message.Content != "Paris" {
		t.Errorf("stored content = %+v, want assembled \"Paris\"", stored.Choices)
	}

	// The shared token parser must read it too.
	if in, out := observability.ParseTokens(fc.setBody); in != 10 || out != 20 {
		t.Errorf("ParseTokens(stored) = (%d, %d), want (10, 20)", in, out)
	}
}

func TestStream_CacheHitReplays(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, fc, fr := newFixture(p)
	fc.entry = &cache.CacheEntry{Response: []byte(okBody), Status: http.StatusOK}

	s, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !s.CacheHit() {
		t.Error("CacheHit() should be true")
	}
	if p.streamCalls != 0 {
		t.Error("provider must not be called on a cache hit")
	}

	got := drain(t, s)
	if len(got) != 2 || got[1] != "[DONE]" {
		t.Fatalf("replay = %q, want one content chunk then [DONE]", got)
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(got[0]), &chunk); err != nil {
		t.Fatalf("replayed chunk is not SSE-shaped: %v (%s)", err, got[0])
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].Delta.Content != "Paris" {
		t.Errorf("replayed delta = %+v, want \"Paris\"", chunk.Choices)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if fc.sets != 0 {
		t.Error("a replayed stream must not rewrite the cache")
	}
	if fr.calls != 0 {
		t.Error("a replayed stream must not record usage")
	}
}

// A stream cut short never sends [DONE]. Caching what arrived would serve a truncated
// answer to every semantically similar prompt from then on.
func TestStream_TruncatedStreamIsNotCached(t *testing.T) {
	truncated := `data: {"choices":[{"delta":{"content":"Par"}}]}

data: {"choices":[{"delta":{"content":"is is the cap`

	p := &fakeProvider{sse: truncated, status: http.StatusOK}
	c, fc, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "k", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drain(t, s)
	s.Close()

	if fc.sets != 0 {
		t.Errorf("cached a truncated answer: %s", fc.setBody)
	}
	// Still metered — the tokens that were consumed upstream are real.
	if fr.calls != 1 {
		t.Errorf("usage recorded %d times, want 1", fr.calls)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// A read failure mid-stream must not look identical to a clean end of stream.
func TestStream_ReadErrorIsReported(t *testing.T) {
	boom := errors.New("connection reset by peer")

	p := &fakeProvider{status: http.StatusOK}
	c, fc, _ := newFixture(p)

	s, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Replace the body with one that yields a chunk and then fails.
	s.scanner = bufio.NewScanner(io.MultiReader(
		strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"Par\"}}]}\n"),
		errReader{err: boom},
	))

	if got := drain(t, s); len(got) != 1 {
		t.Fatalf("got %d chunks, want 1 before the failure", len(got))
	}

	closeErr := s.Close()
	if !errors.Is(closeErr, boom) {
		t.Errorf("Close() = %v, want the read error", closeErr)
	}
	if fc.sets != 0 {
		t.Errorf("cached a stream that failed mid-read: %s", fc.setBody)
	}
}

func TestStream_ProviderError(t *testing.T) {
	p := &fakeProvider{err: errors.New("dial tcp: refused")}
	c, fc, fr := newFixture(p)

	if _, err := c.Stream(context.Background(), Request{Prompt: "hi"}); err == nil {
		t.Fatal("expected an error")
	}
	if fc.sets != 0 || fr.calls != 0 {
		t.Errorf("failed stream stored %d and recorded %d", fc.sets, fr.calls)
	}
}

// The upstream status must survive as far as the caller. Collapsing a 429 into a
// generic error would tell the client "bad gateway" and break its backoff — and would
// leave /chat and /chat/stream disagreeing about the same upstream response.
func TestStream_NonOKUpstreamCarriesTheStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusServiceUnavailable,
		http.StatusUnauthorized,
	} {
		p := &fakeProvider{sse: `{"error":"nope"}`, status: status}
		c, _, _ := newFixture(p)

		_, err := c.Stream(context.Background(), Request{Prompt: "hi"})
		if err == nil {
			t.Fatalf("status %d: expected an error", status)
		}

		var upstream *UpstreamError
		if !errors.As(err, &upstream) {
			t.Fatalf("status %d: got %T (%v), want *UpstreamError", status, err, err)
		}
		if upstream.Status != status {
			t.Errorf("UpstreamError.Status = %d, want %d", upstream.Status, status)
		}
		if !p.lastBody.closed {
			t.Errorf("status %d: upstream body leaked on the error path", status)
		}
	}
}

// A transport failure has no status, so it must not masquerade as one.
func TestStream_TransportErrorIsNotAnUpstreamError(t *testing.T) {
	p := &fakeProvider{err: errors.New("dial tcp: refused")}
	c, _, _ := newFixture(p)

	_, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected an error")
	}

	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		t.Errorf("transport failure reported as UpstreamError with status %d", upstream.Status)
	}
}

func TestStream_CloseIsIdempotent(t *testing.T) {
	p := &fakeProvider{sse: sseBody, status: http.StatusOK}
	c, fc, fr := newFixture(p)

	s, _ := c.Stream(context.Background(), Request{Prompt: "hi"})
	drain(t, s)
	s.Close()
	s.Close()

	if fc.sets != 1 || fr.calls != 1 {
		t.Errorf("double Close stored %d and recorded %d, want 1 each", fc.sets, fr.calls)
	}
}
