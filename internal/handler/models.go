package handler

import (
	"encoding/json"
	"net/http"

	"github.com/ashrafrah96/llm-gateway/internal/router"
)

// ModelInfo is the wire shape of GET /models. It is deliberately separate from
// router.Model so the catalogue can change without breaking the published contract.
type ModelInfo struct {
	ID              string  `json:"id"`
	Description     string  `json:"description"`
	CostPer1KIn     float64 `json:"cost_per_1k_input"`
	CostPer1KOut    float64 `json:"cost_per_1k_output"`
	MaxOutputTokens int     `json:"max_output_tokens"`
}

func models(w http.ResponseWriter, r *http.Request) {
	modelsFor(router.All())(w, r)
}

func modelsFor(all []router.Model) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := make([]ModelInfo, 0, len(all))
		for _, m := range all {
			out = append(out, ModelInfo{
				ID:              m.ID,
				Description:     m.Description,
				CostPer1KIn:     m.PriceIn,
				CostPer1KOut:    m.PriceOut,
				MaxOutputTokens: m.MaxOutputTokens,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}
}
