package ratelimit

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is the second adapter behind Store: it makes the seam real, keeps the
// limiter testable without Redis, and lets the gateway run locally without one.
// Single-process only — use RedisStore when more than one instance is serving.
type MemoryStore struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{hits: make(map[string][]time.Time)}
}

func (s *MemoryStore) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	live := trim(s.hits[key], now.Add(-window))

	if len(live) >= limit {
		s.hits[key] = live
		// The oldest accepted request is the next one to age out.
		return false, live[0].Add(window).Sub(now), nil
	}

	s.hits[key] = append(live, now)
	return true, 0, nil
}

func (s *MemoryStore) Count(ctx context.Context, key string, window time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	live := trim(s.hits[key], time.Now().Add(-window))
	if len(live) == 0 {
		delete(s.hits, key)
		return 0, nil
	}
	s.hits[key] = live
	return len(live), nil
}

// trim drops everything at or before cutoff. Entries are appended in order, so the
// survivors are always a suffix.
//
// ponytail: keys are only pruned when they are touched; add a sweep if idle keys
// accumulate faster than traffic revisits them.
func trim(hits []time.Time, cutoff time.Time) []time.Time {
	for i, t := range hits {
		if t.After(cutoff) {
			return hits[i:]
		}
	}
	return nil
}
