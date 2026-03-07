package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashrafrah96/llm-gateway/internal/provider"
)

func newTestHandler(apiURL string) *Handler {
	return &Handler{
		client: &provider.OpenAIClient{APIKey: "test-key", APIURL: apiURL},
	}
}

func TestChat_InvalidBody(t *testing.T) {
	h := newTestHandler("")
	srv := NewServer(h)

	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChat_MissingPrompt(t *testing.T) {
	h := newTestHandler("")
	srv := NewServer(h)

	body := `{"prompt": ""}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestChat_UpstreamError(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer fake.Close()

	h := newTestHandler(fake.URL)
	srv := NewServer(h)

	body := `{"prompt": "hello"}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestChat_ProxiesResponse(t *testing.T) {
	fakeResp := `{"id":"chatcmpl-123","choices":[{"message":{"content":"Paris"}}]}`

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fakeResp))
	}))
	defer fake.Close()

	h := newTestHandler(fake.URL)
	srv := NewServer(h)

	body := `{"prompt": "What is the capital of France?"}`
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestHealth(t *testing.T) {
	h := newTestHandler("")
	srv := NewServer(h)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestModels(t *testing.T) {
	h := newTestHandler("")
	srv := NewServer(h)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var models []ModelInfo
	if err := json.Unmarshal(w.Body.Bytes(), &models); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(models) == 0 {
		t.Error("expected at least one model")
	}
}
