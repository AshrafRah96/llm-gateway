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
	Status    int       `json:"status"`
}

var costPer1K = map[string]struct{ in, out float64 }{
	"gpt-3.5-turbo": {0.0005, 0.0015},
	"gpt-4":         {0.03, 0.06},
}

func CalculateCost(model string, tokensIn, tokensOut int) float64 {
	costs, ok := costPer1K[model]
	if !ok {
		return 0
	}
	return (float64(tokensIn)/1000)*costs.in + (float64(tokensOut)/1000)*costs.out
}

func Log(r RequestLog) {
	if r.TokensIn > 0 || r.TokensOut > 0 {
		r.CostUSD = CalculateCost(r.Model, r.TokensIn, r.TokensOut)
	}

	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	log.Println(string(data))
}
