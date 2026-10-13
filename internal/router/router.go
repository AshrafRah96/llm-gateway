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

// Models advertises configured primary and fallback models with their actual policy caps.
func (r Router) Models() []Model {
	var result []Model
	for _, m := range []Model{r.Cheap, r.Powerful, r.CheapFallback, r.PowerfulFallback} {
		if m.ID == "" {
			continue
		}
		found := false
		for i, old := range result {
			if old.ID == m.ID {
				result[i].MaxOutputTokens = max(old.MaxOutputTokens, m.MaxOutputTokens)
				found = true
				break
			}
		}
		if !found {
			result = append(result, m)
		}
	}
	return result
}
