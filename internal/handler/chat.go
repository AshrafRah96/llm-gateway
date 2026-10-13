package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/middleware"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"strconv"
	"time"
)

type ChatRequest struct {
	Prompt string `json:"prompt"`
}

const maxPromptBytes = 32 << 10

// Handler is the HTTP adapter. Everything a chat request actually does lives in the
// completion module; these handlers only decode, encode and set headers.
type Handler struct {
	completion *completion.Completion
	usage      StatsReader
	limiter    *ratelimit.Limiter
	ready      func(context.Context) error
}

func (h *Handler) Readiness(check func(context.Context) error) { h.ready = check }

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
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if h.ready == nil || h.ready(ctx) != nil {
			http.Error(w, "dependencies unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /models", modelsFor(h.completion.Models()))

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
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, fmt.Sprintf("request body exceeds %d byte limit", tooLarge.Limit), http.StatusRequestEntityTooLarge)
			return completion.Request{}, false
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return completion.Request{}, false
	}
	if body.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return completion.Request{}, false
	}
	if len(body.Prompt) > maxPromptBytes {
		http.Error(w, "prompt exceeds 32768 byte limit", http.StatusRequestEntityTooLarge)
		return completion.Request{}, false
	}

	req := completion.Request{APIKey: r.Header.Get("X-API-Key"), Prompt: body.Prompt}
	if principal, ok := middleware.PrincipalFromContext(r.Context()); ok {
		req.APIKey = ""
		req.ProjectID = principal.ProjectID
	}
	return req, true
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode(w, r)
	if !ok {
		return
	}

	resp, err := h.completion.Complete(r.Context(), req)
	if err != nil {
		writeCompletionError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.CacheHit {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
		w.Header().Set("X-Model", resp.Model)
	}
	if resp.RAGEnabled && resp.Grounded {
		w.Header().Set("X-RAG-Grounded", "true")
	} else if resp.RAGEnabled {
		w.Header().Set("X-RAG-Grounded", "false")
	}
	if resp.Fallback {
		w.Header().Set("X-Model-Fallback", "true")
	}
	w.WriteHeader(resp.Status)
	w.Write(withGatewayMetadata(resp))
}

type gatewayMetadata struct {
	Grounded bool                `json:"grounded"`
	Sources  []completion.Source `json:"sources,omitempty"`
}

// withGatewayMetadata is additive to OpenAI-shaped JSON. Legacy tests and malformed
// upstream error bodies retain their original bytes rather than hiding provider errors.
func withGatewayMetadata(resp completion.Response) []byte {
	if !resp.RAGEnabled || resp.Status != http.StatusOK {
		return resp.Body
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return resp.Body
	}
	meta, err := json.Marshal(gatewayMetadata{Grounded: resp.Grounded, Sources: resp.Sources})
	if err != nil {
		return resp.Body
	}
	body["gateway"] = meta
	out, err := json.Marshal(body)
	if err != nil {
		return resp.Body
	}
	return out
}

func writeCompletionError(w http.ResponseWriter, err error) {
	var budget *usage.BudgetError
	if errors.As(err, &budget) {
		if budget.Exhausted {
			now := time.Now().UTC()
			seconds := int(now.Truncate(24*time.Hour).Add(24*time.Hour).Sub(now).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Error(w, "project budget exhausted", http.StatusTooManyRequests)
		} else {
			http.Error(w, "project ledger unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	http.Error(w, "upstream error", http.StatusBadGateway)
}
