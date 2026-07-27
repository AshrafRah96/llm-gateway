package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
)

type ChatRequest struct {
	Prompt string `json:"prompt"`
}

// Handler is the HTTP adapter. Everything a chat request actually does lives in the
// completion module; these handlers only decode, encode and set headers.
type Handler struct {
	completion *completion.Completion
	usage      StatsReader
	limiter    *ratelimit.Limiter
}

func New(c *completion.Completion, tracker StatsReader, limiter *ratelimit.Limiter) *Handler {
	return &Handler{completion: c, usage: tracker, limiter: limiter}
}

func NewServer(h *Handler, mws ...middleware.Middleware) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("POST /chat", middleware.Chain(http.HandlerFunc(h.chat), mws...))
	mux.Handle("POST /chat/stream", middleware.Chain(http.HandlerFunc(h.chatStream), mws...))
	mux.Handle("GET /usage", middleware.Chain(usageHandler(h.usage), mws...))
	mux.Handle("GET /limits", middleware.Chain(limitsHandler(h.limiter), mws...))

	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("GET /models", models)

	return mux
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// decode is shared by both entry points so they cannot disagree about what a valid
// chat request looks like.
func decode(w http.ResponseWriter, r *http.Request) (completion.Request, bool) {
	var body ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return completion.Request{}, false
	}
	if body.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return completion.Request{}, false
	}

	return completion.Request{
		APIKey: r.Header.Get("X-API-Key"),
		Prompt: body.Prompt,
	}, true
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}

	resp, err := h.completion.Complete(r.Context(), req)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.CacheHit {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Model", resp.Model)
	}
	w.WriteHeader(resp.Status)
	w.Write(resp.Body)
}
