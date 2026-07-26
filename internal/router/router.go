package router

import "strings"

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
		return Powerful
	}

	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			return Powerful
		}
	}

	return Cheap
}
