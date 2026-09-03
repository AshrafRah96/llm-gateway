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
	return Default().Route(prompt).Primary
}

// Router keeps deterministic routing policy configurable without making routing a
// paid model call. Empty fallbacks mean "return the original upstream result".
type Router struct {
	Cheap            Model
	Powerful         Model
	CheapFallback    Model
	PowerfulFallback Model
}

func Default() Router {
	return Router{
		Cheap:            Cheap,
		Powerful:         Powerful,
		CheapFallback:    Cheap,
		PowerfulFallback: Powerful,
	}
}

func (r Router) Route(prompt string) Decision {
	if r.Cheap.ID == "" {
		r.Cheap = Cheap
	}
	if r.Powerful.ID == "" {
		r.Powerful = Powerful
	}
	lower := strings.ToLower(prompt)

	if len(prompt) > 500 {
		return Decision{Primary: r.Powerful, Fallback: r.PowerfulFallback}
	}

	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			return Decision{Primary: r.Powerful, Fallback: r.PowerfulFallback}
		}
	}

	return Decision{Primary: r.Cheap, Fallback: r.CheapFallback}
}
