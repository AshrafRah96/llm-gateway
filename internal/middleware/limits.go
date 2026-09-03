package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// RequestLimits bounds work before it reaches an embedding or completion provider.
// It is deliberately HTTP-only; model output caps belong in the model catalogue.
func RequestLimits(maxBodyBytes int64, timeout time.Duration, maxConcurrent, maxPerProject int) Middleware {
	sem := make(chan struct{}, maxConcurrent)
	var mu sync.Mutex
	projects := map[string]chan struct{}{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			}
			select {
			case sem <- struct{}{}:
			default:
				http.Error(w, "gateway is at concurrency capacity", http.StatusServiceUnavailable)
				return
			}
			projectID := r.Header.Get("X-API-Key")
			if principal, ok := PrincipalFromContext(r.Context()); ok {
				projectID = principal.ProjectID
			}
			mu.Lock()
			projectSem := projects[projectID]
			if projectSem == nil {
				projectSem = make(chan struct{}, maxPerProject)
				projects[projectID] = projectSem
			}
			mu.Unlock()
			select {
			case projectSem <- struct{}{}:
				defer func() { <-projectSem; <-sem }()
			default:
				<-sem
				http.Error(w, "project is at concurrency capacity", http.StatusTooManyRequests)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
