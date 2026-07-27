package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
)

type fakeStats struct {
	stats *usage.Stats
	err   error
}

func (f fakeStats) Get(ctx context.Context, apiKey string) (*usage.Stats, error) {
	return f.stats, f.err
}

func serverWith(tracker StatsReader, limiter *ratelimit.Limiter) http.Handler {
	c := completion.New(&fakeProvider{}, &fakeCache{}, fakeRecorder{})
	return NewServer(New(c, tracker, limiter))
}

func get(srv http.Handler, path, apiKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestUsage_ReturnsStats(t *testing.T) {
	want := &usage.Stats{Requests: 7, TokensIn: 100, TokensOut: 200, CostUSD: 1.25}
	srv := serverWith(fakeStats{stats: want}, nil)

	w := get(srv, "/usage", "key-1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got usage.Stats
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if got != *want {
		t.Errorf("got %+v, want %+v", got, *want)
	}
}

func TestUsage_MissingKey(t *testing.T) {
	w := get(serverWith(fakeStats{stats: &usage.Stats{}}, nil), "/usage", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestUsage_TrackerError(t *testing.T) {
	w := get(serverWith(fakeStats{err: errors.New("redis down")}, nil), "/usage", "key-1")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestLimits_ReportsRemaining(t *testing.T) {
	l := ratelimit.New(ratelimit.NewMemoryStore(), ratelimit.Config{
		MaxRequests: 10,
		Window:      time.Minute,
	})
	l.Allow(context.Background(), "key-1")
	l.Allow(context.Background(), "key-1")

	srv := serverWith(fakeStats{stats: &usage.Stats{}}, l)

	w := get(srv, "/limits", "key-1")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got LimitStatus
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if got.Limit != 10 || got.Remaining != 8 || got.WindowSec != 60 {
		t.Errorf("got %+v, want limit 10, remaining 8, window 60", got)
	}
}

func TestLimits_MissingKey(t *testing.T) {
	l := ratelimit.New(ratelimit.NewMemoryStore(), ratelimit.DefaultConfig())
	w := get(serverWith(fakeStats{stats: &usage.Stats{}}, l), "/limits", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
