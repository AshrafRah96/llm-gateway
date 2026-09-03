// Package completion owns what a chat request does: route, cache, call the provider,
// meter, log, and store. Both delivery modes cross this one interface.
package completion

import (
	"context"
	"errors"
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
	// APIKey is retained for the legacy in-process seam. Production handlers pass a
	// fingerprinted ProjectID and never forward the raw caller key past auth.
	APIKey    string
	ProjectID string
	Prompt    string
}

// Source is evidence selected by the project retriever. Excerpts are deliberately
// bounded by the retriever so response metadata cannot become a second large payload.
type Source struct {
	DocumentID string `json:"document_id"`
	ChunkID    string `json:"chunk_id"`
	Excerpt    string `json:"excerpt"`
}

type Retrieval struct {
	Prompt        string
	CorpusVersion string
	Sources       []Source
}

// Retriever is intentionally narrow: it supplies untrusted project context, never
// tools or actions. A failed lookup is fail-open as an ungrounded chat response.
type Retriever interface {
	Retrieve(ctx context.Context, projectID, prompt string) (Retrieval, error)
}

type Response struct {
	Body       []byte
	Status     int
	Model      string
	CacheHit   bool
	Grounded   bool
	Sources    []Source
	Fallback   bool
	RAGEnabled bool
}

type Completion struct {
	provider  Provider
	cache     Cache
	usage     Recorder
	router    router.Router
	retriever Retriever
}

// lifecycle concentrates the policy shared by buffered and streaming delivery.
type lifecycle struct {
	completion   *Completion
	ctx          context.Context
	req          Request
	model        router.Model
	fallback     router.Model
	failover     bool
	grounded     bool
	ragEnabled   bool
	sources      []Source
	cacheVersion string
	attempt      cache.Attempt
	started      time.Time
}

func New(provider Provider, cache Cache, usage Recorder) *Completion {
	return NewWithRouting(provider, cache, usage, router.Default(), nil)
}

func NewWithRouting(provider Provider, cache Cache, usage Recorder, r router.Router, retriever Retriever) *Completion {
	return &Completion{provider: provider, cache: cache, usage: usage, router: r, retriever: retriever}
}

func (c *Completion) begin(ctx context.Context, req Request) *lifecycle {
	decision := c.router.Route(req.Prompt)
	l := &lifecycle{
		completion: c,
		ctx:        ctx,
		req:        req,
		model:      decision.Primary,
		fallback:   decision.Fallback,
		started:    time.Now(),
	}
	if c.retriever != nil && req.ProjectID != "" {
		l.ragEnabled = true
		retrieval, err := c.retriever.Retrieve(ctx, req.ProjectID, req.Prompt)
		if err != nil {
			log.Printf("retrieval error: %v", err)
		} else {
			l.cacheVersion = retrieval.CorpusVersion
			l.sources = retrieval.Sources
			l.grounded = len(retrieval.Sources) > 0
			if retrieval.Prompt != "" {
				l.req.Prompt = retrieval.Prompt
			}
		}
	}

	project := req.ProjectID
	if project == "" {
		project = req.APIKey
	}
	namespace := cache.NewProjectNamespace(project, l.model.ID, l.cacheVersion)
	attempt, err := c.cache.Begin(ctx, namespace, req.Prompt)
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
		return Response{Body: entry.Response, Status: entry.Status, CacheHit: true, Grounded: l.grounded, Sources: l.sources, RAGEnabled: l.ragEnabled}, nil
	}

	body, status, err := c.provider.Complete(ctx, l.req.Prompt, l.model)
	if shouldFailover(err, status) && l.fallback.ID != "" && l.fallback.ID != l.model.ID {
		l.failover = true
		body, status, err = c.provider.Complete(ctx, l.req.Prompt, l.fallback)
		if err == nil {
			l.model = l.fallback
		}
	}
	if err != nil {
		return Response{}, err
	}

	tokensIn, tokensOut := observability.ParseTokens(body)
	cost := l.model.Cost(tokensIn, tokensOut)
	l.meter(ctx, usage.Entry{
		APIKey:    l.identity(),
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

	return Response{Body: body, Status: status, Model: l.model.ID, Grounded: l.grounded, Sources: l.sources, Fallback: l.failover, RAGEnabled: l.ragEnabled}, nil
}

func shouldFailover(err error, status int) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func (l *lifecycle) identity() string {
	if l.req.ProjectID != "" {
		return l.req.ProjectID
	}
	return l.req.APIKey
}
