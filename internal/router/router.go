package router

import "strings"

type Model string

const (
	ModelCheap    Model = "gpt-3.5-turbo"
	ModelPowerful Model = "gpt-4"
)

var complexKeywords = []string{
	"analyze",
	"compare",
	"contrast",
	"evaluate",
	"synthesize",
	"code",
	"implement",
	"debug",
	"refactor",
	"step by step",
	"in detail",
}

func Route(prompt string) Model {
	lower := strings.ToLower(prompt)

	if len(prompt) > 500 {
		return ModelPowerful
	}

	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			return ModelPowerful
		}
	}

	return ModelCheap
}
