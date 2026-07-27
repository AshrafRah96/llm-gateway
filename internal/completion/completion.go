// Package completion owns what a chat request does: route, cache, call the provider,
// meter, log, and store. Both delivery modes cross this one interface.
package completion

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

type Provider interface {
	Complete(ctx context.Context, prompt string, model router.Model) ([]byte, int, error)
	Stream(ctx context.Context, prompt string, model router.Model) (ProviderStream, int, error)
}

type ProviderUsage struct {
	PromptTokens     int
	CompletionTokens int
}

type ProviderEvent struct {
	Content string
	Usage   *ProviderUsage
	Done    bool
}

type ProviderStream interface {
	Next() (ProviderEvent, bool)
	Err() error
	Close() error
}

type Cache interface {
	Begin(ctx context.Context, namespace cache.Namespace, prompt string) (cache.Attempt, error)
}

type Recorder interface {
	Record(ctx context.Context, entry usage.Entry) error
}

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
	Model    string
	CacheHit bool
}

type Completion struct {
	provider Provider
	cache    Cache
	usage    Recorder
}

// lifecycle concentrates the policy shared by buffered and streaming delivery.
type lifecycle struct {
	completion *Completion
	ctx        context.Context
	req        Request
	model      router.Model
	namespace  cache.Namespace
	attempt    cache.Attempt
	started    time.Time
}

func New(provider Provider, cache Cache, usage Recorder) *Completion {
	return &Completion{provider: provider, cache: cache, usage: usage}
}

func (c *Completion) begin(ctx context.Context, req Request) *lifecycle {
	model := router.Route(req.Prompt)
	l := &lifecycle{
		completion: c,
		ctx:        ctx,
		req:        req,
		model:      model,
		namespace:  cache.NewNamespace(req.APIKey, model.ID),
		started:    time.Now(),
	}

	attempt, err := c.cache.Begin(ctx, l.namespace, req.Prompt)
	if err != nil {
		log.Printf("cache error: %v", err)
		return l
	}
	l.attempt = attempt
	return l
}

func (l *lifecycle) lookup(ctx context.Context) *cache.CacheEntry {
	if l.attempt == nil {
		return nil
	}
	entry, err := l.attempt.Get(ctx)
	if err != nil {
		log.Printf("cache error: %v", err)
		return nil
	}
	return entry
}

func (l *lifecycle) store(ctx context.Context, body []byte, status int) {
	if l.attempt == nil {
		return
	}
	if err := l.attempt.Set(ctx, body, status); err != nil {
		log.Printf("cache store error: %v", err)
	}
}

func (l *lifecycle) meter(ctx context.Context, entry usage.Entry) {
	if err := l.completion.usage.Record(ctx, entry); err != nil {
		log.Printf("usage record error: %v", err)
	}
}

func (l *lifecycle) log(entry observability.RequestLog) {
	entry.Timestamp = l.started
	entry.LatencyMs = time.Since(l.started).Milliseconds()
	entry.PromptLen = len(l.req.Prompt)
	observability.Log(entry)
}

func (c *Completion) Complete(ctx context.Context, req Request) (Response, error) {
	l := c.begin(ctx, req)

	if entry := l.lookup(ctx); entry != nil {
		l.log(observability.RequestLog{
			CacheHit: true,
			Status:   entry.Status,
		})
		return Response{Body: entry.Response, Status: entry.Status, CacheHit: true}, nil
	}

	body, status, err := c.provider.Complete(ctx, req.Prompt, l.model)
	if err != nil {
		return Response{}, err
	}

	tokensIn, tokensOut := observability.ParseTokens(body)
	cost := l.model.Cost(tokensIn, tokensOut)
	l.meter(ctx, usage.Entry{
		APIKey:    req.APIKey,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		CostUSD:   cost,
	})
	l.log(observability.RequestLog{
		Model:     l.model.ID,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		CostUSD:   cost,
		Status:    status,
	})

	if status == http.StatusOK {
		l.store(ctx, body, status)
	}

	return Response{Body: body, Status: status, Model: l.model.ID}, nil
}
