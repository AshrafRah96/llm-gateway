package middleware

import (
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
)

func RateLimit(limiter *ratelimit.Limiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get("X-API-Key")

			allowed, retryAfter, err := limiter.Allow(r.Context(), apiKey)
			if err != nil {
				http.Error(w, "rate limiter error", http.StatusInternalServerError)
				return
			}

			if !allowed {
				w.Header().Set("Retry-After", retryAfter.String())
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
