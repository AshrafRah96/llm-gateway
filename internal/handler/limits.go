package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
)

type LimitStatus struct {
	Limit     int `json:"limit"`
	Remaining int `json:"remaining"`
	WindowSec int `json:"window_seconds"`
}

func limitsHandler(limiter *ratelimit.Limiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			http.Error(w, "missing X-API-Key", http.StatusUnauthorized)
			return
		}

		count, limit, window, err := limiter.Status(r.Context(), apiKey)
		if err != nil {
			http.Error(w, "failed to get limits", http.StatusInternalServerError)
			return
		}

		remaining := limit - count
		if remaining < 0 {
			remaining = 0
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LimitStatus{
			Limit:     limit,
			Remaining: remaining,
			WindowSec: int(window.Seconds()),
		})
	}
}
