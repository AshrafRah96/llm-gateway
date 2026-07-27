package ratelimit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Lua script cannot be exercised in memory, so this runs against a real Redis and
// skips when there isn't one. Same contract as the MemoryStore tests above.
//
// Dialled once for the whole package so an absent Redis costs one timeout, not one
// per test.
var dial = sync.OnceValues(func() (*redis.Client, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	// MaxRetries -1 so an absent Redis fails once and quietly instead of logging a
	// pool-exhaustion stack on every skipped run.
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("no redis at %s: %w", addr, err)
	}
	return client, nil
})

func redisStore(t *testing.T) *RedisStore {
	t.Helper()

	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	return NewRedisStore(client)
}

func freshKey(t *testing.T, s *RedisStore) string {
	t.Helper()
	key := "ratelimit-test:" + t.Name()
	s.client.Del(context.Background(), key)
	t.Cleanup(func() { s.client.Del(context.Background(), key) })
	return key
}

func TestRedisStore_AllowsUpToTheLimit(t *testing.T) {
	s := redisStore(t)
	key := freshKey(t, s)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		allowed, _, err := s.Allow(ctx, key, 3, time.Minute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}

	allowed, retryAfter, err := s.Allow(ctx, key, 3, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("4th request should be denied")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want a positive value no larger than the window", retryAfter)
	}
}

func TestRedisStore_RejectedRequestsDoNotExtendTheWindow(t *testing.T) {
	s := redisStore(t)
	key := freshKey(t, s)
	ctx := context.Background()

	s.Allow(ctx, key, 2, hammerWindow)
	s.Allow(ctx, key, 2, hammerWindow)

	start := time.Now()
	for time.Since(start) < hammerFor {
		if allowed, _, _ := s.Allow(ctx, key, 2, hammerWindow); allowed {
			t.Fatal("requests over the limit must be denied")
		}
	}

	time.Sleep(settleFor)

	allowed, _, err := s.Allow(ctx, key, 2, hammerWindow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("key never recovered: rejected attempts are still consuming budget")
	}
}

// Every accepted request must occupy its own slot in the window, even when many land
// in the same millisecond. Deriving the sorted-set member from the timestamp collapses
// them into one ZADD, and the limiter then silently lets far more through than the
// limit. Redis Lua numbers are float64, so a nanosecond timestamp cannot be used to
// dodge this — it exceeds the 2^53 exact range and stringifies in scientific notation.
func TestRedisStore_BurstInSameMillisecondCountsIndividually(t *testing.T) {
	s := redisStore(t)
	key := freshKey(t, s)
	ctx := context.Background()

	const burst = 50
	for i := range burst {
		allowed, _, err := s.Allow(ctx, key, burst, time.Minute)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !allowed {
			t.Fatalf("request %d of %d denied: slots are being overwritten", i+1, burst)
		}
	}

	count, err := s.Count(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != burst {
		t.Errorf("window holds %d of %d accepted requests", count, burst)
	}

	// The next one must be refused, which only happens if all 50 really are recorded.
	if allowed, _, _ := s.Allow(ctx, key, burst, time.Minute); allowed {
		t.Error("request over the limit was allowed")
	}
}

func TestRedisStore_CountsAcceptedOnly(t *testing.T) {
	s := redisStore(t)
	key := freshKey(t, s)
	ctx := context.Background()

	s.Allow(ctx, key, 2, time.Minute)
	s.Allow(ctx, key, 2, time.Minute)
	s.Allow(ctx, key, 2, time.Minute) // denied
	s.Allow(ctx, key, 2, time.Minute) // denied

	count, err := s.Count(ctx, key, time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (denied attempts must not be recorded)", count)
	}
}
