package ratelimit

import (
	"context"
	"testing"
	"time"
)

type mockStore struct {
	count int
}

func (m *mockStore) Increment(ctx context.Context, key string, window time.Duration) (int, error) {
	m.count++
	return m.count, nil
}

func (m *mockStore) Count(ctx context.Context, key string, window time.Duration) (int, error) {
	return m.count, nil
}

func TestLimiter_Allow(t *testing.T) {
	store := &mockStore{}
	limiter := New(store, Config{MaxRequests: 3, Window: time.Minute})

	ctx := context.Background()

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		allowed, _, err := limiter.Allow(ctx, "test-key")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	allowed, retryAfter, err := limiter.Allow(ctx, "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("4th request should be denied")
	}
	if retryAfter != time.Minute {
		t.Errorf("retryAfter = %v, want %v", retryAfter, time.Minute)
	}
}

func TestLimiter_Status(t *testing.T) {
	store := &mockStore{count: 5}
	limiter := New(store, Config{MaxRequests: 10, Window: time.Minute})

	count, max, window, err := limiter.Status(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if max != 10 {
		t.Errorf("max = %d, want 10", max)
	}
	if window != time.Minute {
		t.Errorf("window = %v, want %v", window, time.Minute)
	}
}
