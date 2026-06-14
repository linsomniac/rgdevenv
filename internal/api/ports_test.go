package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPortAllocateListReturn(t *testing.T) {
	h := newAPITestHandler(t)

	w := do(h, authReq("POST", "/api/v1/ports/allocate", `{"owner":"x","label":"y"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("allocate: code=%d body=%s", w.Code, w.Body)
	}
	var a map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if a["port"].(float64) != 9000 || a["id"] == "" {
		t.Fatalf("allocate body: %v", a)
	}

	w = do(h, authReq("GET", "/api/v1/ports", ""))
	var lp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &lp)
	if lp["used"].(float64) != 1 {
		t.Fatalf("ports used: %v", lp)
	}

	if w := do(h, authReq("DELETE", "/api/v1/ports/9000", "")); w.Code != http.StatusNoContent {
		t.Fatalf("return: code=%d", w.Code)
	}
	if w := do(h, authReq("DELETE", "/api/v1/ports/9999", "")); w.Code != http.StatusNotFound {
		t.Fatalf("return unknown: code=%d", w.Code)
	}
}

func TestReturnPortInUse(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	body := `{"listen_port":443,"listen_tls":true,"allocate":true}`
	if w := do(h, authReq("POST", "/api/v1/lbs/rg-1.sean.realgo.com/mappings", body)); w.Code != http.StatusCreated {
		t.Fatalf("alloc mapping: code=%d body=%s", w.Code, w.Body)
	}
	if w := do(h, authReq("DELETE", "/api/v1/ports/9000", "")); w.Code != http.StatusConflict {
		t.Fatalf("return in-use: code=%d", w.Code)
	}
}
