package ratelimit

import (
	"context"
	"testing"
	"time"
)

// ponytail: real sleeps rather than an injected clock; add one if these get flaky.
const testWindow = 50 * time.Millisecond

// Timings for the rejected-requests-do-not-extend-the-window test, which needs margin
// on both sides of its assertion:
//
//	hammerFor + settleFor > hammerWindow  — the accepted requests must expire, so a
//	                                        correct limiter lets the key recover;
//	settleFor             < hammerWindow  — a wrongly recorded rejection would still
//	                                        be inside the window, so a broken one does not.
//
// A longer window than testWindow keeps both margins (60ms and 40ms) well clear of
// scheduler jitter.
const (
	hammerWindow = 200 * time.Millisecond
	hammerFor    = hammerWindow / 2
	settleFor    = hammerWindow * 4 / 5
)

func testLimiter(max int) *Limiter {
	return New(NewMemoryStore(), Config{MaxRequests: max, Window: testWindow})
}

func TestLimiter_AllowsUpToTheLimit(t *testing.T) {
	l := testLimiter(3)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		allowed, _, err := l.Allow(ctx, "k")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}

	allowed, retryAfter, err := l.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("4th request should be denied")
	}
	if retryAfter <= 0 || retryAfter > testWindow {
		t.Errorf("retryAfter = %v, want a positive value no larger than %v", retryAfter, testWindow)
	}
}

// The bug the old mock could not see: every rejected attempt used to be recorded, so a
// client that kept retrying kept pushing its own lockout forward and never recovered.
func TestLimiter_RejectedRequestsDoNotExtendTheWindow(t *testing.T) {
	l := New(NewMemoryStore(), Config{MaxRequests: 2, Window: hammerWindow})
	ctx := context.Background()

	l.Allow(ctx, "k")
	l.Allow(ctx, "k")

	start := time.Now()
	for time.Since(start) < hammerFor {
		if allowed, _, _ := l.Allow(ctx, "k"); allowed {
			t.Fatal("requests over the limit must be denied")
		}
	}

	time.Sleep(settleFor)

	allowed, _, err := l.Allow(ctx, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("key never recovered: rejected attempts are still consuming budget")
	}
}

func TestLimiter_WindowSlides(t *testing.T) {
	l := testLimiter(1)
	ctx := context.Background()

	if allowed, _, _ := l.Allow(ctx, "k"); !allowed {
		t.Fatal("first request should be allowed")
	}
	if allowed, _, _ := l.Allow(ctx, "k"); allowed {
		t.Fatal("second request should be denied")
	}

	time.Sleep(testWindow + 10*time.Millisecond)

	if allowed, _, _ := l.Allow(ctx, "k"); !allowed {
		t.Error("request should be allowed once the window has passed")
	}
}

func TestLimiter_KeysAreIndependent(t *testing.T) {
	l := testLimiter(1)
	ctx := context.Background()

	l.Allow(ctx, "a")
	if allowed, _, _ := l.Allow(ctx, "b"); !allowed {
		t.Error("one key's usage must not limit another")
	}
}

// Status reports accepted requests, not attempts.
func TestLimiter_StatusCountsAcceptedOnly(t *testing.T) {
	l := testLimiter(2)
	ctx := context.Background()

	l.Allow(ctx, "k")
	l.Allow(ctx, "k")
	l.Allow(ctx, "k") // denied
	l.Allow(ctx, "k") // denied

	count, max, window, err := l.Status(ctx, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (denied attempts must not be counted)", count)
	}
	if max != 2 {
		t.Errorf("max = %d, want 2", max)
	}
	if window != testWindow {
		t.Errorf("window = %v, want %v", window, testWindow)
	}
}

func TestMemoryStore_ExpiresEntries(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if _, _, err := s.Allow(ctx, "k", 1, testWindow); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count, _ := s.Count(ctx, "k", testWindow); count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	time.Sleep(testWindow + 10*time.Millisecond)

	if count, _ := s.Count(ctx, "k", testWindow); count != 0 {
		t.Errorf("count = %d after the window, want 0", count)
	}
}

func TestMemoryStore_ConcurrentAllowRespectsTheLimit(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	const limit, callers = 5, 50
	results := make(chan bool, callers)
	for range callers {
		go func() {
			allowed, _, _ := s.Allow(ctx, "k", limit, time.Minute)
			results <- allowed
		}()
	}

	granted := 0
	for range callers {
		if <-results {
			granted++
		}
	}
	if granted != limit {
		t.Errorf("granted %d of %d concurrent requests, want exactly %d", granted, callers, limit)
	}
}
