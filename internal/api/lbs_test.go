package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// newAPITestHandler builds a Handler over a real (temp) store with no-op apply
// and always-covered certs. Reused by the other API tests in this package.
func newAPITestHandler(t *testing.T) *Handler { return newAPITestHandlerWith(t, nil) }

func newAPITestHandlerWith(t *testing.T, reporter health.Reporter) *Handler {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := txn.New(st, func(*store.State) {}, func(string) bool { return true }, upstream.NewPolicy(nil),
		txn.Config{PoolStart: 9000, PoolEnd: 9999, HTTPSPort: 443, HTTPPort: 80, MgmtHost: "rgdevenv.sean.realgo.com"})
	return New(Deps{
		Txn: m, Auth: auth.NewAuthenticator(testToken), Limiter: auth.NewRateLimiter(1000, time.Minute),
		CADir: t.TempDir(), Version: "test", HTTPSPort: 443, HTTPPort: 80, PoolStart: 9000, PoolEnd: 9999,
		ActivePorts: func() []int { return []int{443} }, Logger: discardLogger(), Health: reporter,
	})
}

func authReq(method, target, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.RemoteAddr = "127.0.0.1:1"
	return r
}

func do(h *Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestLBLifecycle(t *testing.T) {
	h := newAPITestHandler(t)

	w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"RG-1.sean.realgo.com","label":"demo"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: code=%d body=%s", w.Code, w.Body)
	}
	var lb store.LoadBalancer
	_ = json.Unmarshal(w.Body.Bytes(), &lb)
	if lb.Name != "rg-1.sean.realgo.com" || lb.Label != "demo" {
		t.Fatalf("created LB wrong: %+v", lb)
	}

	if w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"rg-1.sean.realgo.com"}`)); w.Code != http.StatusConflict {
		t.Fatalf("duplicate: code=%d", w.Code)
	}

	w = do(h, authReq("GET", "/api/v1/lbs", ""))
	var list []store.LoadBalancer
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 1 {
		t.Fatalf("list: code=%d n=%d", w.Code, len(list))
	}

	if w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != 200 {
		t.Fatalf("get: code=%d", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs/nope.sean.realgo.com", "")); w.Code != http.StatusNotFound {
		t.Fatalf("get missing: code=%d", w.Code)
	}

	w = do(h, authReq("PATCH", "/api/v1/lbs/rg-1.sean.realgo.com", `{"label":"renamed"}`))
	if w.Code != 200 {
		t.Fatalf("patch: code=%d", w.Code)
	}

	if w := do(h, authReq("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != http.StatusNoContent {
		t.Fatalf("delete: code=%d", w.Code)
	}
	if w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", "")); w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: code=%d", w.Code)
	}
}

func TestCreateLBMissingName(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, authReq("POST", "/api/v1/lbs", `{}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing name: code=%d", w.Code)
	}
}

func TestPatchLBNotFound(t *testing.T) {
	h := newAPITestHandler(t)
	if w := do(h, authReq("PATCH", "/api/v1/lbs/nope.sean.realgo.com", `{"label":"x"}`)); w.Code != http.StatusNotFound {
		t.Fatalf("patch missing LB: code=%d, want 404", w.Code)
	}
}
