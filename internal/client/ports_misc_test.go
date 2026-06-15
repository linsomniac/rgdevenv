package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPortsAndMiscMethods(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/ports" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(PortPool{Start: 9000, End: 9999, Used: 1, Free: 999, Allocations: []Allocation{{ID: "a1", Port: 9000}}})
		case r.URL.Path == "/api/v1/ports/allocate":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(AllocateResult{ID: "a2", Port: 9001})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/v1/cas":
			_ = json.NewEncoder(w).Encode([]string{"corp", "partner"})
		case r.URL.Path == "/api/v1/status":
			_ = json.NewEncoder(w).Encode(Status{Version: "1.0", Upstreams: []UpstreamHealth{}})
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	if pp, err := c.ListPorts(ctx); err != nil || pp.Used != 1 || len(pp.Allocations) != 1 {
		t.Fatalf("ports: %v %+v", err, pp)
	}
	if a, err := c.AllocatePort(ctx, "owner", "label"); err != nil || a.Port != 9001 {
		t.Fatalf("allocate: %v %+v", err, a)
	}
	if err := c.ReturnPort(ctx, 9000); err != nil {
		t.Fatalf("return: %v", err)
	}
	if cas, err := c.ListCAs(ctx); err != nil || len(cas) != 2 || cas[0] != "corp" {
		t.Fatalf("cas: %v %+v", err, cas)
	}
	if s, err := c.Status(ctx); err != nil || s.Version != "1.0" {
		t.Fatalf("status: %v %+v", err, s)
	}
}
