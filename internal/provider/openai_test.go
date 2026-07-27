package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/completion"
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

func TestStreamDecodesEvents(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantEvents []completion.ProviderEvent
		wantErr    bool
	}{
		{
			name: "content usage and done",
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"Paris\"}}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\n" +
				"data: [DONE]\n\n",
			wantEvents: []completion.ProviderEvent{
				{Content: "Paris"},
				{Usage: &completion.ProviderUsage{PromptTokens: 10, CompletionTokens: 20}},
				{Done: true},
			},
		},
		{
			name:       "ignores non data lines",
			body:       ": keepalive\n\ndata: [DONE]\n\n",
			wantEvents: []completion.ProviderEvent{{Done: true}},
		},
		{
			name:    "malformed json reports read error",
			body:    "data: {broken}\n\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := upstream(t, http.StatusOK, tt.body)
			stream, _, err := client.Stream(context.Background(), "hello", router.Cheap)
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			defer stream.Close()

			var got []completion.ProviderEvent
			for event, ok := stream.Next(); ok; event, ok = stream.Next() {
				got = append(got, event)
			}

			if !reflect.DeepEqual(got, tt.wantEvents) {
				t.Errorf("events = %#v, want %#v", got, tt.wantEvents)
			}
			if (stream.Err() != nil) != tt.wantErr {
				t.Errorf("Err() = %v, wantErr %v", stream.Err(), tt.wantErr)
			}
		})
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
