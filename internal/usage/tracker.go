package usage

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type Stats struct {
	Requests  int64   `json:"requests"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
	// EstimatedRequests is how many of Requests had their tokens inferred rather than
	// reported by the provider. See Entry.Estimated.
	EstimatedRequests int64 `json:"estimated_requests"`
}

// Entry is one request's contribution to a key's usage.
type Entry struct {
	APIKey    string
	TokensIn  int
	TokensOut int
	CostUSD   float64

	// Estimated marks a request whose tokens were inferred rather than reported. It
	// happens when a client abandons a stream: cancelling the request kills the
	// upstream read, so the provider's terminal usage chunk never arrives. The cost
	// was still incurred, so it is still charged — but counted separately, so a
	// disputed bill can be explained rather than defended with a number of unknown
	// provenance.
	Estimated bool
}

type Tracker struct {
	client *redis.Client
}

func NewTracker(client *redis.Client) *Tracker {
	return &Tracker{client: client}
}

func (t *Tracker) Record(ctx context.Context, e Entry) error {
	key := "usage:" + e.APIKey

	pipe := t.client.Pipeline()
	pipe.HIncrBy(ctx, key, "requests", 1)
	pipe.HIncrBy(ctx, key, "tokens_in", int64(e.TokensIn))
	pipe.HIncrBy(ctx, key, "tokens_out", int64(e.TokensOut))
	pipe.HIncrByFloat(ctx, key, "cost_usd", e.CostUSD)
	if e.Estimated {
		pipe.HIncrBy(ctx, key, "estimated_requests", 1)
	}

	_, err := pipe.Exec(ctx)
	return err
}

func (t *Tracker) Get(ctx context.Context, apiKey string) (*Stats, error) {
	key := "usage:" + apiKey

	data, err := t.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return &Stats{}, nil
	}

	stats := &Stats{}
	stats.Requests, _ = strconv.ParseInt(data["requests"], 10, 64)
	stats.TokensIn, _ = strconv.ParseInt(data["tokens_in"], 10, 64)
	stats.TokensOut, _ = strconv.ParseInt(data["tokens_out"], 10, 64)
	stats.CostUSD, _ = strconv.ParseFloat(data["cost_usd"], 64)
	stats.EstimatedRequests, _ = strconv.ParseInt(data["estimated_requests"], 10, 64)

	return stats, nil
}
