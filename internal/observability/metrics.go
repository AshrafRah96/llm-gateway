package observability

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

var (
	totalRequests uint64
	cacheHits     uint64
	totalTokens   uint64
)

// TotalRequests is a global counter for total requests received.
var TotalRequests = &counter{&totalRequests}

// CacheHits is a global counter for cache hits.
var CacheHits = &counter{&cacheHits}

// TotalTokens is a global counter for total tokens processed.
var TotalTokens = &counter{&totalTokens}

type counter struct {
	v *uint64
}

func (c *counter) Inc() {
	atomic.AddUint64(c.v, 1)
}

func (c *counter) Add(n float64) {
	atomic.AddUint64(c.v, uint64(n))
}

// MetricsHandler returns an HTTP handler that exposes metrics in Prometheus text format.
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, "# HELP llm_gateway_requests_total Total number of requests received.\n")
		fmt.Fprintf(w, "# TYPE llm_gateway_requests_total counter\n")
		fmt.Fprintf(w, "llm_gateway_requests_total %d\n", atomic.LoadUint64(&totalRequests))
		fmt.Fprintf(w, "# HELP llm_gateway_cache_hits_total Total number of cache hits.\n")
		fmt.Fprintf(w, "# TYPE llm_gateway_cache_hits_total counter\n")
		fmt.Fprintf(w, "llm_gateway_cache_hits_total %d\n", atomic.LoadUint64(&cacheHits))
		fmt.Fprintf(w, "# HELP llm_gateway_tokens_total Total number of tokens processed (prompt plus completion combined).\n")
		fmt.Fprintf(w, "# TYPE llm_gateway_tokens_total counter\n")
		fmt.Fprintf(w, "llm_gateway_tokens_total %d\n", atomic.LoadUint64(&totalTokens))
	})
}
