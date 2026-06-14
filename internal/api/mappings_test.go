package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
)

func mustCreateLB(t *testing.T, h *Handler, name string) {
	t.Helper()
	if w := do(h, authReq("POST", "/api/v1/lbs", `{"name":"`+name+`"}`)); w.Code != http.StatusCreated {
		t.Fatalf("create LB %s: code=%d body=%s", name, w.Code, w.Body)
	}
}

func TestMappingCreateReplaceDelete(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")

	body := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("create mapping: code=%d body=%s", w.Code, w.Body)
	}
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusConflict {
		t.Fatalf("dup mapping: code=%d", w.Code)
	}
	put := `{"listen_port":443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9099,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("PUT", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", put)); w.Code != http.StatusOK {
		t.Fatalf("put mapping: code=%d body=%s", w.Code, w.Body)
	}
	bad := `{"listen_port":8443,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9099,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("PUT", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", bad)); w.Code != http.StatusBadRequest {
		t.Fatalf("port mismatch: code=%d", w.Code)
	}
	if w := do(h, authReq("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", "")); w.Code != http.StatusNoContent {
		t.Fatalf("delete mapping: code=%d", w.Code)
	}
}

func TestMappingAllocateConvenience(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-2.sean.realgo.com")
	body := `{"listen_port":443,"listen_tls":true,"allocate":true,"label":"web"}`
	w := do(h, authReq("POST", "/api/v1/lbs/rg-2.sean.realgo.com/mappings", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("allocate mapping: code=%d body=%s", w.Code, w.Body)
	}
	var m store.Mapping
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if !m.AutoAllocated || m.Upstream.Host != "localhost" || m.Upstream.Port != 9000 {
		t.Fatalf("allocate convenience wrong: %+v", m)
	}
}

func TestMappingListenPortInPool(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-3.sean.realgo.com")
	body := `{"listen_port":9500,"listen_tls":true,"upstream":{"scheme":"http","host":"localhost","port":9011,"tls":{"mode":"verify"}}}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-3.sean.realgo.com/mappings", body)); w.Code != http.StatusConflict {
		t.Fatalf("listen_port in pool: code=%d", w.Code)
	}
}

func TestDeleteMappingNotFound(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	if w := do(h, authReq("DELETE", "/api/v1/lbs/rg-1.sean.realgo.com/mappings/443", "")); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing mapping: code=%d, want 404", w.Code)
	}
}
