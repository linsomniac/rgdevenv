package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/health"
	"github.com/realgo/rgdevenv/internal/store"
)

type fakeHealth struct{ s health.Status }

func (f fakeHealth) Status(store.Upstream) health.Status { return f.s }
func (f fakeHealth) List() []health.Entry {
	return []health.Entry{{Scheme: "http", Host: "localhost", Port: 9011, Health: f.s}}
}

func addMapping(t *testing.T, h *Handler, lb string) {
	t.Helper()
	body := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/"+lb+"/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("add mapping: %d %s", w.Code, w.Body)
	}
}

func TestLBHealthDefaultsUnknown(t *testing.T) {
	h := newAPITestHandler(t) // no reporter → noop → unknown
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	addMapping(t, h, "rg-1.sean.realgo.com")

	w := do(h, authReq("GET", "/api/v1/lbs", ""))
	if !strings.Contains(w.Body.String(), `"health":"unknown"`) {
		t.Fatalf("expected per-mapping health unknown, got %s", w.Body)
	}
}

func TestLBAndStatusHealthFromReporter(t *testing.T) {
	h := newAPITestHandlerWith(t, fakeHealth{health.Up})
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	addMapping(t, h, "rg-1.sean.realgo.com")

	w := do(h, authReq("GET", "/api/v1/lbs/rg-1.sean.realgo.com", ""))
	if !strings.Contains(w.Body.String(), `"health":"up"`) {
		t.Fatalf("expected mapping health up, got %s", w.Body)
	}
	w = do(h, authReq("GET", "/api/v1/status", ""))
	if !strings.Contains(w.Body.String(), `"upstreams"`) || !strings.Contains(w.Body.String(), `"health":"up"`) {
		t.Fatalf("expected status upstreams with health up, got %s", w.Body)
	}
}
