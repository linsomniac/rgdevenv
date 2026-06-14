package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/txn"
)

const testToken = "0123456789abcdef0123456789abcdef"

func testHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{
		auth:    auth.NewAuthenticator(testToken),
		limiter: auth.NewRateLimiter(3, time.Minute),
		version: "test",
		logger:  discardLogger(),
	}
}

func TestAuthMiddlewareRejectsAndRateLimits(t *testing.T) {
	h := testHandler(t)
	protected := h.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No token -> 401.
	r := httptest.NewRequest("GET", "/api/v1/x", nil)
	r.RemoteAddr = "9.9.9.9:1234"
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: code = %d, want 401", w.Code)
	}

	// Repeated failures from one IP -> 429 (limiter limit = 3).
	bad := func() int {
		rr := httptest.NewRequest("GET", "/api/v1/x", nil)
		rr.RemoteAddr = "8.8.8.8:1"
		ww := httptest.NewRecorder()
		protected.ServeHTTP(ww, rr)
		return ww.Code
	}
	for i := 0; i < 3; i++ {
		if got := bad(); got != http.StatusUnauthorized {
			t.Fatalf("failure %d: code = %d, want 401", i, got)
		}
	}
	if got := bad(); got != http.StatusTooManyRequests {
		t.Fatalf("after 3 failures: code = %d, want 429", got)
	}

	// A valid token from a rate-limited IP is still blocked (limit precedes auth).
	r2 := httptest.NewRequest("GET", "/api/v1/x", nil)
	r2.RemoteAddr = "8.8.8.8:1"
	r2.Header.Set("Authorization", "Bearer "+testToken)
	w2 := httptest.NewRecorder()
	protected.ServeHTTP(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("good token from rate-limited IP: code = %d, want 429", w2.Code)
	}

	// Good token -> 200 (fresh IP not yet rate-limited).
	r = httptest.NewRequest("GET", "/api/v1/x", nil)
	r.RemoteAddr = "1.1.1.1:1"
	r.Header.Set("Authorization", "Bearer "+testToken)
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("good token: code = %d, want 200", w.Code)
	}
}

func TestHealthzUnauthenticated(t *testing.T) {
	h := New(Deps{Auth: auth.NewAuthenticator(testToken), Limiter: auth.NewRateLimiter(10, time.Minute), Logger: discardLogger()})
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz code = %d, want 200", w.Code)
	}
}

func TestWriteErrMapping(t *testing.T) {
	h := testHandler(t)
	cases := []struct {
		err  error
		want int
	}{
		{txn.Validation("bad", "x"), http.StatusBadRequest},
		{txn.Conflict("dup", "x"), http.StatusConflict},
		{txn.NotFound("nf", "x"), http.StatusNotFound},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.writeErr(w, c.err)
		if w.Code != c.want {
			t.Fatalf("err %v -> code %d, want %d", c.err, w.Code, c.want)
		}
		var body map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] == "" || body["error"] == "" {
			t.Fatalf("error body missing fields: %s", w.Body.String())
		}
	}
}
