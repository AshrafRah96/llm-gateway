package ratelimit

import (
	"context"
	"time"
)

type Config struct {
	MaxRequests int
	Window      time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxRequests: 10,
		Window:      time.Minute,
	}
}

type Limiter struct {
	store  Store
	config Config
}

func New(store Store, config Config) *Limiter {
	return &Limiter{
		store:  store,
		config: config,
	}
}

func (l *Limiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	count, err := l.store.Increment(ctx, key, l.config.Window)
	if err != nil {
		return false, 0, err
	}

	if count > l.config.MaxRequests {
		return false, l.config.Window, nil
	}

	return true, 0, nil
}

func (l *Limiter) Status(ctx context.Context, key string) (int, int, time.Duration, error) {
	count, err := l.store.Count(ctx, key, l.config.Window)
	if err != nil {
		return 0, 0, 0, err
	}
	return count, l.config.MaxRequests, l.config.Window, nil
}
