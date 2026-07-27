package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/ashrafrah96/llm-gateway/internal/ratelimit"
)

func limited(max int) Middleware {
	return RateLimit(ratelimit.New(
		ratelimit.NewMemoryStore(),
		ratelimit.Config{MaxRequests: max, Window: time.Minute},
	))
}

func call(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("X-API-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRateLimit_AllowsThenDenies(t *testing.T) {
	var ran bool
	h := limited(2)(okHandler(&ran))

	for i := 1; i <= 2; i++ {
		if w := call(h, "k"); w.Code != http.StatusOK {
			t.Errorf("request %d = %d, want 200", i, w.Code)
		}
	}

	w := call(h, "k")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
}

// Retry-After must be delta-seconds. time.Duration.String() emits "1m0s", which no
// client can parse.
func TestRateLimit_RetryAfterIsAnInteger(t *testing.T) {
	var ran bool
	h := limited(1)(okHandler(&ran))

	call(h, "k")
	w := call(h, "k")

	got := w.Header().Get("Retry-After")
	if got == "" {
		t.Fatal("Retry-After not set on a 429")
	}

	seconds, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("Retry-After = %q, which is not delta-seconds: %v", got, err)
	}
	if seconds < 1 || seconds > 60 {
		t.Errorf("Retry-After = %d, want between 1 and the 60s window", seconds)
	}
}

func TestRateLimit_KeysAreIndependent(t *testing.T) {
	var ran bool
	h := limited(1)(okHandler(&ran))

	call(h, "a")
	if w := call(h, "b"); w.Code != http.StatusOK {
		t.Errorf("second key = %d, want 200", w.Code)
	}
}
