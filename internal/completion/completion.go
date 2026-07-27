// Package completion owns what a chat request does: look in the cache, pick a model,
// call the provider, meter the result, log it, store it. Both HTTP entry points cross
// this one interface, so neither can drift from the other.
package completion

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

// Provider is the upstream model API. Satisfied by *provider.OpenAIClient in
// production and by a fake in tests.
type Provider interface {
	Complete(ctx context.Context, prompt string, m router.Model) ([]byte, int, error)
	Stream(ctx context.Context, prompt string, m router.Model) (io.ReadCloser, int, error)
}

// Cache is the semantic prompt cache. Satisfied by *cache.SemanticCache.
type Cache interface {
	Get(ctx context.Context, ns cache.Namespace, prompt string) (*cache.CacheEntry, error)
	Set(ctx context.Context, ns cache.Namespace, prompt string, response []byte, status int) error
}

// Recorder meters a request against an API key. Satisfied by *usage.Tracker.
type Recorder interface {
	Record(ctx context.Context, e usage.Entry) error
}

// UpstreamError reports that the provider answered with a non-success status. It carries
// the status so adapters can propagate it instead of flattening everything to 502 —
// a client that receives 429 can back off; one that receives 502 cannot.
//
// A transport failure (no response at all) is a plain error, not this.
type UpstreamError struct {
	Status int
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream returned %d", e.Status)
}

type Request struct {
	APIKey string
	Prompt string
}

type Response struct {
	Body     []byte
	Status   int
	Model    string // empty on a cache hit because this request made no provider call
	CacheHit bool
}

type Completion struct {
	provider Provider
	cache    Cache
	usage    Recorder
}

// New requires all three collaborators. There are no nil checks downstream because
// there is no way to build a Completion without them.
func New(p Provider, c Cache, u Recorder) *Completion {
	return &Completion{provider: p, cache: c, usage: u}
}

func (c *Completion) Complete(ctx context.Context, req Request) (Response, error) {
	start := time.Now()
	model := router.Route(req.Prompt)
	ns := cache.NewNamespace(req.APIKey, model.ID)

	if entry := c.lookup(ctx, ns, req.Prompt); entry != nil {
		observability.Log(observability.RequestLog{
			Timestamp: start,
			LatencyMs: time.Since(start).Milliseconds(),
			CacheHit:  true,
			PromptLen: len(req.Prompt),
			Status:    entry.Status,
		})
		return Response{Body: entry.Response, Status: entry.Status, CacheHit: true}, nil
	}

	body, status, err := c.provider.Complete(ctx, req.Prompt, model)
	if err != nil {
		return Response{}, err
	}

	tokensIn, tokensOut := observability.ParseTokens(body)
	c.meter(ctx, usage.Entry{
		APIKey:    req.APIKey,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		CostUSD:   model.Cost(tokensIn, tokensOut),
	})

	observability.Log(observability.RequestLog{
		Timestamp: start,
		LatencyMs: time.Since(start).Milliseconds(),
		Model:     model.ID,
		PromptLen: len(req.Prompt),
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		CostUSD:   model.Cost(tokensIn, tokensOut),
		Status:    status,
	})

	if status == http.StatusOK {
		c.store(ctx, ns, req.Prompt, body, status)
	}

	return Response{Body: body, Status: status, Model: model.ID}, nil
}

// lookup returns nil on a miss. A cache failure degrades to a miss rather than
// failing the request — the upstream call is the source of truth.
func (c *Completion) lookup(ctx context.Context, ns cache.Namespace, prompt string) *cache.CacheEntry {
	entry, err := c.cache.Get(ctx, ns, prompt)
	if err != nil {
		log.Printf("cache error: %v", err)
		return nil
	}
	return entry
}

func (c *Completion) store(ctx context.Context, ns cache.Namespace, prompt string, body []byte, status int) {
	if err := c.cache.Set(ctx, ns, prompt, body, status); err != nil {
		log.Printf("cache store error: %v", err)
	}
}

// meter records the request even when the upstream failed: `requests` counts attempts,
// and a failed body parses to zero tokens so it costs nothing.
func (c *Completion) meter(ctx context.Context, e usage.Entry) {
	if err := c.usage.Record(ctx, e); err != nil {
		log.Printf("usage record error: %v", err)
	}
}
