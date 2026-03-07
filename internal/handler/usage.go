package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

func usageHandler(tracker *usage.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			http.Error(w, "missing X-API-Key", http.StatusUnauthorized)
			return
		}

		stats, err := tracker.Get(r.Context(), apiKey)
		if err != nil {
			http.Error(w, "failed to get usage", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}
