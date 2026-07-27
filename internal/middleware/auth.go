package middleware

import (
	"context"
	"net/http"
)

// KeyValidator is satisfied by *auth.KeyStore in production and by a fake in tests.
type KeyValidator interface {
	Valid(ctx context.Context, apiKey string) (bool, error)
}

func Auth(store KeyValidator) Middleware {
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
