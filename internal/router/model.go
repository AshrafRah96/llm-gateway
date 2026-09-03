package router

// Model is the catalogue entry for an upstream model: what it is called, what it is
// good for, and what it costs. This is the only place any of those three facts lives.
type Model struct {
	ID              string
	Description     string
	PriceIn         float64 // USD per 1K input tokens
	PriceOut        float64 // USD per 1K output tokens
	MaxOutputTokens int
}

// Decision is the policy result for one prompt. Fallback is deliberately another model
// at the same provider: this project demonstrates safe failover without becoming a
// multi-provider control plane.
type Decision struct {
	Primary  Model
	Fallback Model
}

var (
	Cheap = Model{
		ID:              "gpt-3.5-turbo",
		Description:     "Fast, good for simple tasks",
		PriceIn:         0.0005,
		PriceOut:        0.0015,
		MaxOutputTokens: 512,
	}
	Powerful = Model{
		ID:              "gpt-4",
		Description:     "Powerful, good for complex tasks",
		PriceIn:         0.03,
		PriceOut:        0.06,
		MaxOutputTokens: 1024,
	}
)

// All returns every model the gateway will route to.
func All() []Model {
	return []Model{Cheap, Powerful}
}

// Cost is the USD price of a completion of this size on this model.
func (m Model) Cost(tokensIn, tokensOut int) float64 {
	return (float64(tokensIn)/1000)*m.PriceIn + (float64(tokensOut)/1000)*m.PriceOut
}
