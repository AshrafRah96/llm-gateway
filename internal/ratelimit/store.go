package ratelimit

import (
	"context"
	"time"
)

type Store interface {
	Increment(ctx context.Context, key string, window time.Duration) (count int, err error)
	Count(ctx context.Context, key string, window time.Duration) (count int, err error)
}
