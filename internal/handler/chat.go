package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/cache"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/observability"
	"github.com/ashrafrah96/llm-gateway/internal/provider"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/router"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

type ChatRequest struct {
	Prompt string `json:"prompt"`
}

type Handler struct {
	client  *provider.OpenAIClient
	cache   *cache.SemanticCache
	usage   *usage.Tracker
	limiter *ratelimit.Limiter
}

func New(client *provider.OpenAIClient, semanticCache *cache.SemanticCache, tracker *usage.Tracker, limiter *ratelimit.Limiter) *Handler {
	return &Handler{
		client:  client,
		cache:   semanticCache,
		usage:   tracker,
		limiter: limiter,
	}
}

func NewServer(h *Handler, mws ...middleware.Middleware) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("POST /chat", middleware.Chain(http.HandlerFunc(h.chat), mws...))
	mux.Handle("POST /chat/stream", middleware.Chain(http.HandlerFunc(h.chatStream), mws...))
	mux.Handle("GET /usage", middleware.Chain(usageHandler(h.usage), mws...))
	mux.Handle("GET /limits", middleware.Chain(limitsHandler(h.limiter), mws...))

	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /models", models)
	mux.Handle("GET /metrics", observability.MetricsHandler())

	return mux
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	observability.TotalRequests.Inc()

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	if h.cache != nil {
		entry, err := h.cache.Get(r.Context(), req.Prompt)
		if err != nil {
			log.Printf("cache error: %v", err)
		} else if entry != nil {
			observability.CacheHits.Inc()
			observability.Log(observability.RequestLog{
				Timestamp: start,
				LatencyMs: time.Since(start).Milliseconds(),
				CacheHit:  true,
				PromptLen: len(req.Prompt),
				Status:    entry.Status,
			})
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			w.WriteHeader(entry.Status)
			w.Write(entry.Response)
			return
		}
	}

	model := router.Route(req.Prompt)

	body, status, err := h.client.Call(req.Prompt, string(model))
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	tokensIn, tokensOut := observability.ParseTokens(body)
	if tokensIn > 0 || tokensOut > 0 {
		observability.TotalTokens.Add(float64(tokensIn + tokensOut))
	}
	cost := observability.CalculateCost(string(model), tokensIn, tokensOut)

	if h.usage != nil {
		apiKey := r.Header.Get("X-API-Key")
		if err := h.usage.Record(r.Context(), apiKey, tokensIn, tokensOut, cost); err != nil {
			log.Printf("usage record error: %v", err)
		}
	}

	observability.Log(observability.RequestLog{
		Timestamp: start,
		LatencyMs: time.Since(start).Milliseconds(),
		Model:     string(model),
		CacheHit:  false,
		PromptLen: len(req.Prompt),
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		Status:    status,
	})

	if h.cache != nil && status == http.StatusOK {
		if err := h.cache.Set(r.Context(), req.Prompt, body, status); err != nil {
			log.Printf("cache store error: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Header().Set("X-Model", string(model))
	w.WriteHeader(status)
	w.Write(body)
}
