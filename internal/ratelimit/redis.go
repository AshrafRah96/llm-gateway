package ratelimit

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Increment(ctx context.Context, key string, window time.Duration) (int, error) {
	now := time.Now()
	cutoff := now.Add(-window)

	pipe := s.client.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(cutoff.UnixNano(), 10))
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: now.UnixNano(),
	})
	countCmd := pipe.ZCard(ctx, key)
	pipe.Expire(ctx, key, window)

	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}

	return int(countCmd.Val()), nil
}

func (s *RedisStore) Count(ctx context.Context, key string, window time.Duration) (int, error) {
	cutoff := time.Now().Add(-window)
	count, err := s.client.ZCount(ctx, key, strconv.FormatInt(cutoff.UnixNano(), 10), "+inf").Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}
