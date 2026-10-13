package usage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Tracker is an adapter over Redis counters, so there is nothing to verify without one.
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

func tracker(t *testing.T) (*Tracker, string) {
	t.Helper()

	client, err := dial()
	if err != nil {
		t.Skip(err)
	}

	apiKey := "usage-test:" + t.Name()
	cleanProject(client, apiKey)
	t.Cleanup(func() { cleanProject(client, apiKey) })

	return NewTracker(client), apiKey
}

func TestTracker_RecordAccumulates(t *testing.T) {
	tr, key := tracker(t)
	ctx := context.Background()

	for i := range 3 {
		err := tr.Record(ctx, Entry{APIKey: key, EventID: fmt.Sprintf("e-%d", i), TokensIn: 10, TokensOut: 20, CostUSD: 0.5})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	got, err := tr.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	want := Stats{Requests: 3, TokensIn: 30, TokensOut: 60, CostUSD: 1.5, TotalTokens: 90, TotalCostUSD: 1.5}
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

// An estimated request still counts towards the bill — the cost was real — but it must
// be distinguishable, so a disputed charge can be explained.
func TestTracker_EstimatedRequestsAreCountedSeparately(t *testing.T) {
	tr, key := tracker(t)
	ctx := context.Background()

	if err := tr.Record(ctx, Entry{APIKey: key, EventID: "measured", TokensIn: 100, TokensOut: 200, CostUSD: 1}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := tr.Record(ctx, Entry{
		APIKey: key, EventID: "estimated", TokensIn: 50, TokensOut: 25, CostUSD: 0.25, Estimated: true,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := tr.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	want := Stats{
		Requests:          2,
		TokensIn:          150,
		TokensOut:         225,
		CostUSD:           1.25,
		EstimatedRequests: 1,
		TotalTokens:       375, TotalCostUSD: 1.25,
	}
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

func TestTracker_GetUnknownKey(t *testing.T) {
	tr, _ := tracker(t)

	got, err := tr.Get(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if *got != (Stats{}) {
		t.Errorf("got %+v, want zero stats", *got)
	}
}

func cleanProject(client *redis.Client, project string) {
	iter := client.Scan(context.Background(), 0, prefix(project)+"*", 100).Iterator()
	for iter.Next(context.Background()) {
		client.Del(context.Background(), iter.Val())
	}
}
