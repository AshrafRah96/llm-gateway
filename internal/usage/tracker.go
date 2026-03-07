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
}

type Tracker struct {
	client *redis.Client
}

func NewTracker(client *redis.Client) *Tracker {
	return &Tracker{client: client}
}

func (t *Tracker) Record(ctx context.Context, apiKey string, tokensIn, tokensOut int, cost float64) error {
	key := "usage:" + apiKey

	pipe := t.client.Pipeline()
	pipe.HIncrBy(ctx, key, "requests", 1)
	pipe.HIncrBy(ctx, key, "tokens_in", int64(tokensIn))
	pipe.HIncrBy(ctx, key, "tokens_out", int64(tokensOut))
	pipe.HIncrByFloat(ctx, key, "cost_usd", cost)

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

	return stats, nil
}
