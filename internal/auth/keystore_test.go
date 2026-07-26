package auth

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyStore is a thin adapter over Redis, so there is nothing to verify without one.
// Dialled once for the package so an absent Redis costs one timeout, not one per test.
var dial = sync.OnceValues(func() (*redis.Client, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("no redis at %s: %w", addr, err)
	}
	return client, nil
})

func store(t *testing.T) (*KeyStore, *redis.Client) {
	t.Helper()

	client, err := dial()
	if err != nil {
		t.Skip(err)
	}
	// Seeding goes through the client, not the store: provisioning is somebody else's
	// job, and Valid is the whole interface.
	client.Del(context.Background(), keyName)
	t.Cleanup(func() { client.Del(context.Background(), keyName) })

	return NewKeyStore(client), client
}

func TestKeyStore_Valid(t *testing.T) {
	s, client := store(t)
	ctx := context.Background()

	if err := client.SAdd(ctx, keyName, "live-key").Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, tt := range []struct {
		key  string
		want bool
	}{
		{"live-key", true},
		{"unknown-key", false},
		{"", false},
		{"LIVE-KEY", false}, // keys are compared exactly, not case-insensitively
	} {
		got, err := s.Valid(ctx, tt.key)
		if err != nil {
			t.Fatalf("Valid(%q): %v", tt.key, err)
		}
		if got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestKeyStore_ValidWithNoKeysConfigured(t *testing.T) {
	s, _ := store(t)

	got, err := s.Valid(context.Background(), "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("an empty key set must not authorise anything")
	}
}
