package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListCAsEndpoint(t *testing.T) {
	h := newAPITestHandler(t)
	if err := os.WriteFile(filepath.Join(h.caDir, "corp.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := do(h, authReq("GET", "/api/v1/cas", ""))
	var names []string
	_ = json.Unmarshal(w.Body.Bytes(), &names)
	if w.Code != 200 || len(names) != 1 || names[0] != "corp" {
		t.Fatalf("cas: code=%d names=%v", w.Code, names)
	}
}

func TestStatusEndpoint(t *testing.T) {
	h := newAPITestHandler(t)
	mustCreateLB(t, h, "rg-1.sean.realgo.com")
	w := do(h, authReq("GET", "/api/v1/status", ""))
	var s map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s["version"] != "test" || s["load_balancers"].(float64) != 1 {
		t.Fatalf("status: code=%d body=%v", w.Code, s)
	}
}
