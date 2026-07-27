package observability

import (
	"encoding/json"
	"log"
	"time"
)

type RequestLog struct {
	Timestamp time.Time `json:"timestamp"`
	LatencyMs int64     `json:"latency_ms"`
	Model     string    `json:"model,omitempty"`
	CacheHit  bool      `json:"cache_hit"`
	PromptLen int       `json:"prompt_len"`
	TokensIn  int       `json:"tokens_in,omitempty"`
	TokensOut int       `json:"tokens_out,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	// Estimated marks token counts inferred rather than reported by the provider,
	// which happens when a client abandons a stream. The charge is real; the precision
	// is not, and the log has to say so.
	Estimated bool `json:"estimated,omitempty"`
	Status    int  `json:"status"`
}

// Log emits one structured line. Pricing is the Model catalogue's job — callers pass
// the cost they already computed rather than having it recomputed here.
func Log(r RequestLog) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	log.Println(string(data))
}
