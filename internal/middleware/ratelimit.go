package middleware

import (
	"math"
	"net/http"
	"strconv"
	"time"

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
				w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// retryAfterSeconds renders RFC 9110 delta-seconds. time.Duration.String() emits Go
// syntax ("1m0s"), which no client can parse. Rounds up so the client never retries
// a moment early, and never advertises 0.
func retryAfterSeconds(d time.Duration) string {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
