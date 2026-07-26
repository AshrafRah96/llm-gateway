package completion

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

const (
	maxSSELine = 1 << 20

	// Close runs after the client may already have gone away, so it detaches from the
	// request context. This bounds the detached work instead of leaving it unbounded.
	settleTimeout = 5 * time.Second
)

type usageTotals struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// sseChunk is the streaming wire shape. Usage only arrives on the terminal chunk, and
// only because provider.Stream asks for stream_options.include_usage.
type sseChunk struct {
	Choices []streamChoice `json:"choices"`
	Usage   *usageTotals   `json:"usage"`
}

type streamChoice struct {
	Delta delta `json:"delta"`
}

type delta struct {
	Content string `json:"content"`
}

// completionBody is the non-streaming wire shape. Streams assemble into it so a streamed
// answer and a /chat answer share one cache entry format and can serve each other.
type completionBody struct {
	Choices []completionChoice `json:"choices"`
	Usage   usageTotals        `json:"usage"`
}

type completionChoice struct {
	Message message `json:"message"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Stream yields SSE payloads while accumulating everything Close needs in order to
// meter, log and cache the exchange. One pass over the body serves all three.
type Stream struct {
	c     *Completion
	ctx   context.Context
	req   Request
	model router.Model
	start time.Time

	body    interface{ Close() error }
	scanner *bufio.Scanner

	cacheHit bool
	replay   []string

	content   strings.Builder
	tokensIn  int
	tokensOut int

	complete bool  // saw [DONE]; a truncated stream must not be cached
	metered  bool  // the provider reported usage; otherwise Close has to estimate
	err      error // a read failure, kept distinct from a clean end of stream
	done     bool
	closed   bool
}

func (c *Completion) Stream(ctx context.Context, req Request) (*Stream, error) {
	s := &Stream{c: c, ctx: ctx, req: req, start: time.Now()}

	if entry := c.lookup(ctx, req.Prompt); entry != nil {
		if chunks, ok := replayChunks(entry); ok {
			s.cacheHit, s.replay = true, chunks
			return s, nil
		}
		// An entry we cannot read is a miss, not a failure.
	}

	s.model = router.Route(req.Prompt)

	body, status, err := c.provider.Stream(ctx, req.Prompt, s.model)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		body.Close()
		return nil, &UpstreamError{Status: status}
	}

	s.body = body
	s.scanner = bufio.NewScanner(body)
	s.scanner.Buffer(make([]byte, 0, 64*1024), maxSSELine)
	return s, nil
}

func (s *Stream) Model() string  { return s.model.ID }
func (s *Stream) CacheHit() bool { return s.cacheHit }

// Next returns the next SSE payload to write, or ok=false once the stream is spent.
// The value is the part after "data: ", including the final "[DONE]".
func (s *Stream) Next() (string, bool) {
	if s.done {
		return "", false
	}

	if s.cacheHit {
		if len(s.replay) == 0 {
			s.done = true
			return "", false
		}
		data := s.replay[0]
		s.replay = s.replay[1:]
		return data, true
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			s.done, s.complete = true, true
			return data, true
		}

		// The terminal usage chunk exists because we asked for it, not because the
		// client did, and it carries an empty choices array. Take its numbers and keep
		// it out of the client's stream.
		if s.absorb(data) {
			continue
		}
		return data, true
	}

	// Scan stopped: either the body ended cleanly or the read failed. Only the former
	// is a whole answer, and Close needs to tell them apart.
	s.err = s.scanner.Err()
	s.done = true
	return "", false
}

// absorb keeps the running answer and the token totals current as chunks go past.
// It reports whether the chunk was ours alone — a usage report with nothing in it for
// the client — in which case the caller must not forward it.
func (s *Stream) absorb(data string) (internalOnly bool) {
	var chunk sseChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false
	}
	for _, c := range chunk.Choices {
		s.content.WriteString(c.Delta.Content)
	}
	if chunk.Usage == nil {
		return false
	}

	s.tokensIn = chunk.Usage.PromptTokens
	s.tokensOut = chunk.Usage.CompletionTokens
	s.metered = true

	// Usage always arrives on a chunk with no choices. Should a provider ever attach it
	// to one carrying content, forward it rather than swallow the content.
	return len(chunk.Choices) == 0
}

// Close meters, logs and caches the exchange. Safe to call twice — handlers defer it.
func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true

	if s.body != nil {
		s.body.Close()
	}

	entry := observability.RequestLog{
		Timestamp: s.start,
		LatencyMs: time.Since(s.start).Milliseconds(),
		PromptLen: len(s.req.Prompt),
		Status:    http.StatusOK,
	}

	if s.cacheHit {
		entry.CacheHit = true
		observability.Log(entry)
		return nil
	}

	// No usage chunk means the client abandoned the stream: cancelling the request
	// killed the upstream read before the provider could report. Tokens were still
	// consumed — the prompt is charged in full, and whatever was generated before the
	// cut is charged too — so estimate rather than record a zero that quietly writes
	// off a real cost.
	if !s.metered {
		s.tokensIn = estimateTokens(s.req.Prompt)
		s.tokensOut = estimateTokens(s.content.String())
	}

	entry.Model = s.model.ID
	entry.TokensIn = s.tokensIn
	entry.TokensOut = s.tokensOut
	entry.CostUSD = s.model.Cost(s.tokensIn, s.tokensOut)
	entry.Estimated = !s.metered
	observability.Log(entry)

	// A client that disconnects mid-stream still gets billed, so the metering and the
	// cache write must outlive the request context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), settleTimeout)
	defer cancel()

	s.c.meter(ctx, usage.Entry{
		APIKey:    s.req.APIKey,
		TokensIn:  s.tokensIn,
		TokensOut: s.tokensOut,
		CostUSD:   s.model.Cost(s.tokensIn, s.tokensOut),
		Estimated: !s.metered,
	})

	// Only a stream that reached [DONE] is a whole answer. Caching a truncated one
	// would serve it to every semantically similar prompt from then on.
	if !s.complete {
		if s.err != nil {
			log.Printf("stream read error: %v", s.err)
		}
		return s.err
	}
	if body, ok := assemble(s.content.String(), s.tokensIn, s.tokensOut); ok {
		s.c.store(ctx, s.req.Prompt, body, http.StatusOK)
	}
	return nil
}

// estimateTokens approximates a token count for the fallback path where the provider
// never reported one. Used only for abandoned streams, and every charge derived from it
// is flagged usage.Entry.Estimated so it is never mistaken for a measurement.
//
// ponytail: ~4 bytes per token, the usual rule of thumb for English; it under-reads
// CJK. Swap in a real BPE tokenizer if reconciliation against provider invoices shows
// the drift matters.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// assemble turns the accumulated deltas into the body a non-streaming call would have
// produced. An empty answer is not worth caching.
func assemble(content string, tokensIn, tokensOut int) ([]byte, bool) {
	if content == "" {
		return nil, false
	}

	body, err := json.Marshal(completionBody{
		Choices: []completionChoice{{Message: message{Role: "assistant", Content: content}}},
		Usage:   usageTotals{PromptTokens: tokensIn, CompletionTokens: tokensOut},
	})
	if err != nil {
		return nil, false
	}
	return body, true
}

// replayChunks turns a stored completion back into streaming shape.
//
// ponytail: cached streams replay as one chunk; split it if clients depend on
// incremental delivery.
func replayChunks(entry *cache.CacheEntry) ([]string, bool) {
	var stored completionBody
	if err := json.Unmarshal(entry.Response, &stored); err != nil {
		return nil, false
	}
	if len(stored.Choices) == 0 || stored.Choices[0].Message.Content == "" {
		return nil, false
	}

	data, err := json.Marshal(sseChunk{
		Choices: []streamChoice{{Delta: delta{Content: stored.Choices[0].Message.Content}}},
	})
	if err != nil {
		return nil, false
	}
	return []string{string(data), "[DONE]"}, true
}
