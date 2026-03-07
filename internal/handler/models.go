package handler

import (
	"encoding/json"
	"net/http"
)

type ModelInfo struct {
	ID           string  `json:"id"`
	Description  string  `json:"description"`
	CostPer1KIn  float64 `json:"cost_per_1k_input"`
	CostPer1KOut float64 `json:"cost_per_1k_output"`
}

var availableModels = []ModelInfo{
	{
		ID:           "gpt-3.5-turbo",
		Description:  "Fast, good for simple tasks",
		CostPer1KIn:  0.0005,
		CostPer1KOut: 0.0015,
	},
	{
		ID:           "gpt-4",
		Description:  "Powerful, good for complex tasks",
		CostPer1KIn:  0.03,
		CostPer1KOut: 0.06,
	},
}

func models(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(availableModels)
}
