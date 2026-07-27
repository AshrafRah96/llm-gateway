package ratelimit

import (
	"context"
	"time"
)

// Store owns the sliding window. It answers the question rather than supplying an
// ingredient: a store that only counted would force the caller to record before it
// could decide, which is how rejected requests used to consume budget.
//
// Allow records the request only when it is accepted. retryAfter is the time until the
// oldest accepted request ages out, and is meaningful only when allowed is false.
type Store interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
	Count(ctx context.Context, key string, window time.Duration) (count int, err error)
}
