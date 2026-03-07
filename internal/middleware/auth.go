package middleware

import (
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/auth"
)

func Auth(store *auth.KeyStore) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")
			if apiKey == "" {
				http.Error(w, "missing X-API-Key", http.StatusUnauthorized)
				return
			}

			valid, err := store.Valid(r.Context(), apiKey)
			if err != nil {
				http.Error(w, "auth error", http.StatusInternalServerError)
				return
			}
			if !valid {
				http.Error(w, "invalid API key", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
