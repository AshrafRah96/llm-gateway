package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/router"
)

// captured is what the fake upstream saw.
type captured struct {
	auth        string
	contentType string
	body        map[string]any
}

func upstream(t *testing.T, status int, respBody string) (*OpenAIClient, *captured) {
	t.Helper()
	got := &captured{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.auth = r.Header.Get("Authorization")
		got.contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &got.body)

		w.WriteHeader(status)
		w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)

	return &OpenAIClient{APIKey: "test-key", APIURL: srv.URL}, got
}

func TestComplete_SendsAuthAndModel(t *testing.T) {
	c, got := upstream(t, http.StatusOK, `{"usage":{"prompt_tokens":3}}`)

	body, status, err := c.Complete(context.Background(), "hello", router.Powerful)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != `{"usage":{"prompt_tokens":3}}` {
		t.Errorf("body = %s", body)
	}

	if got.auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	if got.body["model"] != router.Powerful.ID {
		t.Errorf("model = %v, want %s", got.body["model"], router.Powerful.ID)
	}
	if _, ok := got.body["stream"]; ok {
		t.Error("non-streaming call must not set stream")
	}
}

func TestStream_RequestsUsage(t *testing.T) {
	c, got := upstream(t, http.StatusOK, "data: [DONE]\n\n")

	stream, status, err := c.Stream(context.Background(), "hello", router.Cheap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if got.body["stream"] != true {
		t.Error("streaming call must set stream:true")
	}

	// Without this the terminal usage chunk never arrives and streams stay unmetered.
	opts, ok := got.body["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("streaming call must send stream_options")
	}
	if opts["include_usage"] != true {
		t.Errorf("include_usage = %v, want true", opts["include_usage"])
	}
}

// An upstream 4xx/5xx is a status to propagate, not a transport error.
func TestComplete_UpstreamStatusIsNotAnError(t *testing.T) {
	c, _ := upstream(t, http.StatusTooManyRequests, `{"error":"slow down"}`)

	body, status, err := c.Complete(context.Background(), "hi", router.Cheap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", status)
	}
	if string(body) != `{"error":"slow down"}` {
		t.Errorf("body = %s", body)
	}
}

func TestComplete_HonoursContext(t *testing.T) {
	c, _ := upstream(t, http.StatusOK, `{}`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := c.Complete(ctx, "hi", router.Cheap); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestStream_HonoursContext(t *testing.T) {
	c, _ := upstream(t, http.StatusOK, "data: [DONE]\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := c.Stream(ctx, "hi", router.Cheap); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
