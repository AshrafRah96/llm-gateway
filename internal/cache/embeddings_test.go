package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/money"
	"github.com/ashrafrah96/llm-gateway/internal/usage"
	"github.com/redis/go-redis/v9"
)

type embeddingTrackingBody struct {
	io.ReadCloser
	read   int
	closed bool
}

func TestMeteredEmbeddingRequiresMeasuredUsage(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	redisClient := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	t.Cleanup(func() { redisClient.Close() })
	pingCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		t.Skipf("Redis is not reachable: %v", err)
	}
	ledger := usage.NewTracker(redisClient)
	tests := []struct {
		name    string
		usage   string
		wantErr string
		tokens  int
		pending int
		kind    string
	}{
		{name: "absent usage", wantErr: "missing prompt_tokens usage", tokens: 10, pending: 1},
		{name: "null usage", usage: `,"usage":null`, wantErr: "missing prompt_tokens usage", tokens: 10, pending: 1},
		{name: "absent prompt tokens", usage: `,"usage":{"total_tokens":2}`, wantErr: "missing prompt_tokens usage", tokens: 10, pending: 1},
		{name: "null prompt tokens", usage: `,"usage":{"prompt_tokens":null}`, wantErr: "missing prompt_tokens usage", tokens: 10, pending: 1},
		{name: "negative usage", usage: `,"usage":{"prompt_tokens":-1}`, wantErr: "exceeds reserved bounds", tokens: 10, pending: 1},
		{name: "excess usage", usage: `,"usage":{"prompt_tokens":11}`, wantErr: "exceeds reserved bounds", tokens: 10, pending: 1},
		{name: "measured usage", usage: `,"usage":{"prompt_tokens":2}`, tokens: 2},
		{name: "explicit zero", usage: `,"usage":{"prompt_tokens":0}`, tokens: 0},
		{name: "measured retrieval", usage: `,"usage":{"prompt_tokens":2}`, tokens: 2, kind: "rag_lookup"},
		{name: "measured import", usage: `,"usage":{"prompt_tokens":2}`, tokens: 2, kind: "rag_import"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := "embedding-usage-test:" + rand.Text()
			hash := sha256.Sum256([]byte(project))
			prefix := "ledger:{" + hex.EncodeToString(hash[:]) + "}:"
			t.Cleanup(func() {
				iter := redisClient.Scan(context.Background(), 0, prefix+"*", 100).Iterator()
				for iter.Next(context.Background()) {
					if err := redisClient.Del(context.Background(), iter.Val()).Err(); err != nil {
						t.Errorf("clean ledger: %v", err)
					}
				}
				if err := iter.Err(); err != nil {
					t.Errorf("scan ledger: %v", err)
				}
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("X-Client-Request-Id") == "" {
					t.Error("missing reservation request ID")
				}
				_, _ = io.WriteString(w, `{"data":[{"embedding":[0.25,-0.5]}]`+tt.usage+`}`)
			}))
			t.Cleanup(server.Close)
			kind := tt.kind
			if kind == "" {
				kind = "cache_lookup"
			}
			client := NewMeteredEmbeddingClient("test-key", ledger, kind)
			client.url = server.URL
			ctx := usage.WithProject(context.Background(), project)
			vector, err := client.Embed(ctx, "test input")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || vector != nil {
					t.Fatalf("Embed = %v, %v; want nil vector, %q", vector, err, tt.wantErr)
				}
			} else if err != nil || !reflect.DeepEqual(vector, []float32{0.25, -0.5}) {
				t.Fatalf("Embed = %v, %v", vector, err)
			}
			pending, err := ledger.Pending(ctx, project)
			if err != nil || len(pending) != tt.pending {
				t.Fatalf("pending = %+v, %v; want %d", pending, err, tt.pending)
			}
			if len(pending) > 0 && (pending[0].Tokens != 10 || pending[0].CostNanos != 200) {
				t.Fatalf("uncertain reservation changed: %+v", pending[0])
			}
			stats, err := ledger.Get(ctx, project)
			if err != nil || stats.EmbeddingRequests != 1 || stats.EmbeddingTokens != int64(tt.tokens) || stats.EmbeddingCostUSD != money.USD(int64(tt.tokens)*20) {
				t.Fatalf("embedding accounting = %+v, %v; want %d tokens", stats, err, tt.tokens)
			}
			events, err := redisClient.HGetAll(ctx, prefix+"events").Result()
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%+v, %v", events, err)
			}
			for _, raw := range events {
				var event usage.Entry
				if json.Unmarshal([]byte(raw), &event) != nil || event.Kind != kind {
					t.Fatalf("embedding operation kind lost: %s", raw)
				}
			}
		})
	}
}

func (b *embeddingTrackingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += n
	return n, err
}

func (b *embeddingTrackingBody) Close() error {
	b.closed = true
	return b.ReadCloser.Close()
}

type embeddingTrackingTransport struct {
	base http.RoundTripper
	body *embeddingTrackingBody
}

func (t *embeddingTrackingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		t.body = &embeddingTrackingBody{ReadCloser: resp.Body}
		resp.Body = t.body
	}
	return resp, err
}

func TestEmbeddingResponseBoundsAndValidation(t *testing.T) {
	valid := `{"data":[{"embedding":[0.25,-0.5]}]}`
	tests := []struct {
		name   string
		status int
		body   string
		err    string
	}{
		{name: "exact cap", status: http.StatusOK, body: valid + strings.Repeat(" ", embeddingResponseLimit-len(valid))},
		{name: "success overflow", status: http.StatusOK, body: valid + strings.Repeat(" ", embeddingResponseLimit+1-len(valid)), err: "embedding response exceeds"},
		{name: "error overflow", status: http.StatusBadGateway, body: strings.Repeat("x", 2*embeddingResponseLimit), err: "embedding response exceeds"},
		{name: "error exact cap", status: http.StatusBadGateway, body: strings.Repeat("x", embeddingResponseLimit), err: "api error:"},
		{name: "malformed JSON", status: http.StatusOK, body: `{"data":`, err: "unmarshal:"},
		{name: "no data", status: http.StatusOK, body: `{"data":[]}`, err: "no embedding returned"},
		{name: "missing vector", status: http.StatusOK, body: `{"data":[{}]}`, err: "no embedding returned"},
		{name: "empty vector", status: http.StatusOK, body: `{"data":[{"embedding":[]}]}`, err: "no embedding returned"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("unexpected embedding request: %s, auth %q", req.Method, req.Header.Get("Authorization"))
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(server.Close)
			transport := &embeddingTrackingTransport{base: server.Client().Transport}
			previous := http.DefaultClient
			http.DefaultClient = &http.Client{Transport: transport}
			t.Cleanup(func() { http.DefaultClient = previous })
			client := NewEmbeddingClient("test-key")
			client.url = server.URL
			vector, err := client.Embed(context.Background(), "test input")
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
			} else if err != nil || !reflect.DeepEqual(vector, []float32{0.25, -0.5}) {
				t.Fatalf("Embed = %v, %v", vector, err)
			}
			if transport.body == nil || !transport.body.closed {
				t.Fatal("response body not closed")
			}
			wantRead := min(len(tt.body), embeddingResponseLimit+1)
			if transport.body.read != wantRead {
				t.Fatalf("read %d bytes, want %d", transport.body.read, wantRead)
			}
		})
	}
}
