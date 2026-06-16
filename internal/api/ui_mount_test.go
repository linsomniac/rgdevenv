package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func unauthReq(method, target string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "127.0.0.1:1"
	return r
}

func TestUIServedAtRootWithoutAuth(t *testing.T) {
	h := newAPITestHandler(t)
	w := do(h, unauthReq("GET", "/"))
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content-type = %q, want text/html", ct)
	}
}

func TestUIMountDoesNotShadowAPIAuth(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, unauthReq("GET", "/api/v1/lbs")); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth /api/v1/lbs = %d, want 401 (UI mount must not bypass auth)", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs", "")); w.Code != http.StatusOK {
		t.Fatalf("auth /api/v1/lbs = %d, want 200", w.Code)
	}
}

func TestHealthzStillOpenAfterUIMount(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, unauthReq("GET", "/healthz")); w.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", w.Code)
	}
}
