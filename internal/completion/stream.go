package completion

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

const settleTimeout = 5 * time.Second

type usageTotals struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
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

type StreamChunk struct {
	Content string
	Done    bool
}

// Stream yields provider-neutral content chunks while accumulating everything Close
// needs in order to meter, log, and cache the exchange.
type Stream struct {
	lifecycle *lifecycle

	body ProviderStream

	cacheHit bool
	replay   []StreamChunk

	content   strings.Builder
	tokensIn  int
	tokensOut int

	complete  bool
	metered   bool
	err       error
	done      bool
	closed    bool
	settled   bool
	settleErr error
}

func (c *Completion) Stream(ctx context.Context, req Request) (*Stream, error) {
	l := c.begin(ctx, req)
	s := &Stream{lifecycle: l}

	if entry := l.lookup(ctx); entry != nil {
		if chunks, ok := replayChunks(entry); ok {
			s.cacheHit, s.replay = true, chunks
			return s, nil
		}
		// An entry we cannot read is a miss, not a failure.
	}

	body, status, err := c.provider.Stream(ctx, req.Prompt, l.model)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		body.Close()
		return nil, &UpstreamError{Status: status}
	}

	s.body = body
	return s, nil
}

func (s *Stream) Model() string  { return s.lifecycle.model.ID }
func (s *Stream) CacheHit() bool { return s.cacheHit }

func (s *Stream) Next() (StreamChunk, bool) {
	if s.done {
		return StreamChunk{}, false
	}

	if s.cacheHit {
		if len(s.replay) == 0 {
			s.done = true
			return StreamChunk{}, false
		}
		chunk := s.replay[0]
		s.replay = s.replay[1:]
		if chunk.Done {
			s.done = true
			_ = s.settle()
		}
		return chunk, true
	}

	for {
		event, ok := s.body.Next()
		if !ok {
			s.err = s.body.Err()
			s.done = true
			_ = s.settle()
			return StreamChunk{}, false
		}
		if event.Usage != nil {
			s.tokensIn = event.Usage.PromptTokens
			s.tokensOut = event.Usage.CompletionTokens
			s.metered = true
		}
		if event.Done {
			s.done, s.complete = true, true
			_ = s.settle()
			return StreamChunk{Done: true}, true
		}
		if event.Content != "" {
			s.content.WriteString(event.Content)
			return StreamChunk{Content: event.Content}, true
		}
	}
}

// Close settles a stream only when exhaustion has not already done so. It is the
// fallback for abandonment and is safe to call twice.
func (s *Stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.settle()
}

func (s *Stream) settle() error {
	if s.settled {
		return s.settleErr
	}
	s.settled = true

	if s.body != nil {
		s.body.Close()
	}

	entry := observability.RequestLog{Status: http.StatusOK}

	if s.cacheHit {
		entry.CacheHit = true
		s.lifecycle.log(entry)
		return nil
	}

	if !s.metered {
		s.tokensIn = estimateTokens(s.lifecycle.req.Prompt)
		s.tokensOut = estimateTokens(s.content.String())
	}

	entry.Model = s.lifecycle.model.ID
	entry.TokensIn = s.tokensIn
	entry.TokensOut = s.tokensOut
	entry.CostUSD = s.lifecycle.model.Cost(s.tokensIn, s.tokensOut)
	entry.Estimated = !s.metered
	s.lifecycle.log(entry)

	// Settlement must outlive a request cancelled by a disconnected client, but it is
	// bounded so abandoned streams cannot create unbounded background work.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.lifecycle.ctx), settleTimeout)
	defer cancel()

	s.lifecycle.meter(ctx, usage.Entry{
		APIKey:    s.lifecycle.req.APIKey,
		TokensIn:  s.tokensIn,
		TokensOut: s.tokensOut,
		CostUSD:   s.lifecycle.model.Cost(s.tokensIn, s.tokensOut),
		Estimated: !s.metered,
	})

	// A provider stream is cacheable only after its explicit terminal event.
	if !s.complete {
		if s.err != nil {
			log.Printf("stream read error: %v", s.err)
		}
		s.settleErr = s.err
		return s.settleErr
	}
	if body, ok := assemble(s.content.String(), s.tokensIn, s.tokensOut); ok {
		s.lifecycle.store(ctx, body, http.StatusOK)
	}
	return s.settleErr
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

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

func replayChunks(entry *cache.CacheEntry) ([]StreamChunk, bool) {
	var stored completionBody
	if err := json.Unmarshal(entry.Response, &stored); err != nil {
		return nil, false
	}
	if len(stored.Choices) == 0 || stored.Choices[0].Message.Content == "" {
		return nil, false
	}

	return []StreamChunk{
		{Content: stored.Choices[0].Message.Content},
		{Done: true},
	}, true
}
