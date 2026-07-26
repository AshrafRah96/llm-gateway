package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeKeys struct {
	valid  bool
	err    error
	got    string
	called bool
}

func (f *fakeKeys) Valid(ctx context.Context, apiKey string) (bool, error) {
	f.called = true
	f.got = apiKey
	return f.valid, f.err
}

func okHandler(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuth_MissingKey(t *testing.T) {
	keys := &fakeKeys{valid: true}
	var ran bool

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	w := httptest.NewRecorder()
	Auth(keys)(okHandler(&ran)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if keys.called {
		t.Error("store should not be consulted without a key")
	}
	if ran {
		t.Error("next handler should not run")
	}
}

func TestAuth_StoreError(t *testing.T) {
	keys := &fakeKeys{err: errors.New("redis down")}
	var ran bool

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.Header.Set("X-API-Key", "k")
	w := httptest.NewRecorder()
	Auth(keys)(okHandler(&ran)).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	if ran {
		t.Error("next handler should not run")
	}
}

func TestAuth_InvalidKey(t *testing.T) {
	keys := &fakeKeys{valid: false}
	var ran bool

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.Header.Set("X-API-Key", "nope")
	w := httptest.NewRecorder()
	Auth(keys)(okHandler(&ran)).ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if ran {
		t.Error("next handler should not run")
	}
}

func TestAuth_ValidKey(t *testing.T) {
	keys := &fakeKeys{valid: true}
	var ran bool

	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	req.Header.Set("X-API-Key", "secret")
	w := httptest.NewRecorder()
	Auth(keys)(okHandler(&ran)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !ran {
		t.Error("next handler should run")
	}
	if keys.got != "secret" {
		t.Errorf("store saw key %q, want %q", keys.got, "secret")
	}
}
