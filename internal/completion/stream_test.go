package completion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/router"
)

var completeEvents = []ProviderEvent{
	{Content: "Par"},
	{Content: "is"},
	{Usage: &ProviderUsage{PromptTokens: 10, CompletionTokens: 20}},
	{Done: true},
}

func drain(t *testing.T, s *Stream) []StreamChunk {
	t.Helper()
	var out []StreamChunk
	for chunk, ok := s.Next(); ok; chunk, ok = s.Next() {
		out = append(out, chunk)
	}
	return out
}

func TestStreamSettlesWhenExhausted(t *testing.T) {
	tests := []struct {
		name          string
		events        []ProviderEvent
		cacheEntry    *cache.CacheEntry
		wantRecords   int
		wantStores    int
		wantEstimated bool
	}{
		{
			name:        "complete provider stream",
			events:      completeEvents,
			wantRecords: 1,
			wantStores:  1,
		},
		{
			name: "truncated provider stream",
			events: []ProviderEvent{
				{Content: "partial answer"},
			},
			wantRecords:   1,
			wantEstimated: true,
		},
		{
			name:       "cache replay",
			cacheEntry: &cache.CacheEntry{Response: []byte(okBody), Status: http.StatusOK},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &fakeProvider{events: tt.events, status: http.StatusOK}
			c, fc, fr := newFixture(p)
			fc.entry = tt.cacheEntry

			s, err := c.Stream(context.Background(), Request{APIKey: "key-1", Prompt: "hello"})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			drain(t, s)

			if fr.calls != tt.wantRecords {
				t.Errorf("usage records = %d, want %d", fr.calls, tt.wantRecords)
			}
			if fc.sets != tt.wantStores {
				t.Errorf("cache stores = %d, want %d", fc.sets, tt.wantStores)
			}
			if fr.estimated != tt.wantEstimated {
				t.Errorf("estimated = %v, want %v", fr.estimated, tt.wantEstimated)
			}
		})
	}
}

func TestStream_PassesChunksThroughInOrder(t *testing.T) {
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
	c, _, _ := newFixture(p)

	s, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.Close()

	got := drain(t, s)
	if len(got) != 3 {
		t.Fatalf("got %d chunks: %+v", len(got), got)
	}
	if got[0].Content != "Par" {
		t.Errorf("first chunk = %+v", got[0])
	}
	if !got[len(got)-1].Done {
		t.Errorf("last chunk = %+v, want Done", got[len(got)-1])
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
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
	c, _, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "k", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := drain(t, s)
	if len(got) != 3 {
		t.Fatalf("forwarded %d chunks, want two content chunks and Done", len(got))
	}
	for _, chunk := range got[:2] {
		if chunk.Content == "" || chunk.Done {
			t.Errorf("forwarded non-content provider event: %+v", chunk)
		}
	}

	// Swallowed, not ignored: the tokens still have to reach the meter.
	s.Close()
	if fr.in != 10 || fr.out != 20 {
		t.Errorf("recorded in=%d out=%d, want 10/20", fr.in, fr.out)
	}
}

// This is the regression the review found: streams used to be unmetered entirely.
func TestStream_ExhaustionRecordsUsageAndClosesProvider(t *testing.T) {
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
	c, _, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "key-1", Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drain(t, s)

	if fr.calls != 1 {
		t.Fatalf("usage recorded %d times, want 1", fr.calls)
	}
	if fr.key != "key-1" || fr.in != 10 || fr.out != 20 {
		t.Errorf("recorded key=%q in=%d out=%d", fr.key, fr.in, fr.out)
	}
	if want := router.Cheap.Cost(10, 20); fr.cost != want {
		t.Errorf("cost = %v, want %v", fr.cost, want)
	}
	if !p.lastStream.closed {
		t.Error("upstream stream was not closed")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close after automatic settlement: %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("Close recorded usage again; calls = %d", fr.calls)
	}
}

// A streamed answer must land in the cache in the same shape a /chat answer does,
// so either entry point can serve the other's traffic.
func TestStream_CloseCachesAssembledCompletion(t *testing.T) {
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
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
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
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
	if len(got) != 2 || !got[1].Done {
		t.Fatalf("replay = %+v, want one content chunk then Done", got)
	}
	if got[0].Content != "Paris" {
		t.Errorf("replayed content = %q, want Paris", got[0].Content)
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
	p := &fakeProvider{
		events: []ProviderEvent{
			{Content: "Par"},
			{Content: "is is the cap"},
		},
		status: http.StatusOK,
	}
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

// A read failure mid-stream must not look identical to a clean end of stream.
func TestStream_ReadErrorIsReported(t *testing.T) {
	boom := errors.New("connection reset by peer")

	p := &fakeProvider{
		events:  []ProviderEvent{{Content: "Par"}},
		readErr: boom,
		status:  http.StatusOK,
	}
	c, fc, _ := newFixture(p)

	s, err := c.Stream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

// Cancelling a stream kills the upstream read, so the provider's terminal usage chunk
// never arrives. Recording zero would hide a cost we really incurred — the prompt was
// charged in full and tokens were generated before the cut.
func TestStream_AbandonedStreamIsEstimatedNotZero(t *testing.T) {
	prompt := "What is the capital of France, and what is it known for?"

	p := &fakeProvider{
		events: []ProviderEvent{
			{Content: "Paris is the capital"},
			{Content: " of France and also"},
		},
		status: http.StatusOK,
	}
	c, fc, fr := newFixture(p)

	s, err := c.Stream(context.Background(), Request{APIKey: "k", Prompt: prompt})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drain(t, s)
	s.Close()

	if fr.calls != 1 {
		t.Fatalf("usage recorded %d times, want 1", fr.calls)
	}
	if !fr.estimated {
		t.Error("an inferred charge must be marked estimated")
	}
	if fr.in == 0 {
		t.Error("prompt tokens recorded as zero; the prompt is charged in full even on cancellation")
	}
	if fr.out == 0 {
		t.Error("completion tokens recorded as zero; tokens were generated before the cut")
	}
	if fr.cost <= 0 {
		t.Errorf("cost = %v, want a positive charge", fr.cost)
	}

	// Still not cached: an estimate is good enough to bill, not to serve.
	if fc.sets != 0 {
		t.Error("a truncated answer must not be cached")
	}
}

func TestStream_CompletedStreamIsNotMarkedEstimated(t *testing.T) {
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
	c, _, fr := newFixture(p)

	s, _ := c.Stream(context.Background(), Request{APIKey: "k", Prompt: "hi"})
	drain(t, s)
	s.Close()

	if fr.estimated {
		t.Error("a stream that reported real usage must not be marked estimated")
	}
	if fr.in != 10 || fr.out != 20 {
		t.Errorf("recorded in=%d out=%d, want the reported 10/20", fr.in, fr.out)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens(""); got != 0 {
		t.Errorf("estimateTokens(\"\") = %d, want 0", got)
	}
	// Roughly four characters per token, always at least one for non-empty input.
	if got := estimateTokens("a"); got != 1 {
		t.Errorf("estimateTokens(\"a\") = %d, want 1", got)
	}
	if got := estimateTokens("12345678"); got != 2 {
		t.Errorf("estimateTokens(8 chars) = %d, want 2", got)
	}
	// Monotonic: more text never estimates fewer tokens.
	prev := 0
	for _, s := range []string{"a", "ab", "abcd", "abcdefgh", "abcdefghijklmnop"} {
		got := estimateTokens(s)
		if got < prev {
			t.Errorf("estimateTokens(%q) = %d, less than the shorter string's %d", s, got, prev)
		}
		prev = got
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
		p := &fakeProvider{status: status}
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
		if !p.lastStream.closed {
			t.Errorf("status %d: upstream stream leaked on the error path", status)
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
	p := &fakeProvider{events: completeEvents, status: http.StatusOK}
	c, fc, fr := newFixture(p)

	s, _ := c.Stream(context.Background(), Request{Prompt: "hi"})
	drain(t, s)
	s.Close()
	s.Close()

	if fc.sets != 1 || fr.calls != 1 {
		t.Errorf("double Close stored %d and recorded %d, want 1 each", fc.sets, fr.calls)
	}
}
