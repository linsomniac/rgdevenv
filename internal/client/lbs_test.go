package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLBMethods(t *testing.T) {
	var lastMethod, lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			lastBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/lbs":
			_ = json.NewEncoder(w).Encode([]LoadBalancer{{Name: "a.example.com", Mappings: []Mapping{}}})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(LoadBalancer{Name: "a.example.com", Label: "demo"})
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	lbs, err := c.ListLBs(ctx)
	if err != nil || len(lbs) != 1 || lbs[0].Name != "a.example.com" {
		t.Fatalf("list: %v %+v", err, lbs)
	}
	lb, err := c.CreateLB(ctx, "a.example.com", "demo")
	if err != nil || lb.Label != "demo" {
		t.Fatalf("create: %v %+v", err, lb)
	}
	if lastMethod != http.MethodPost || lastPath != "/api/v1/lbs" || lastBody == "" {
		t.Fatalf("create request wrong: %s %s %s", lastMethod, lastPath, lastBody)
	}
	if _, err := c.SetLBLabel(ctx, "a.example.com", "renamed"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if lastMethod != http.MethodPatch {
		t.Fatalf("set method = %s", lastMethod)
	}
	if err := c.DeleteLB(ctx, "a.example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if lastMethod != http.MethodDelete || lastPath != "/api/v1/lbs/a.example.com" {
		t.Fatalf("delete request wrong: %s %s", lastMethod, lastPath)
	}
}

func TestGetLB(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(LoadBalancer{Name: "a.example.com", Label: "demo", Mappings: []Mapping{}})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	lb, err := c.GetLB(context.Background(), "a.example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v1/lbs/a.example.com" {
		t.Fatalf("get request wrong: %s %s", gotMethod, gotPath)
	}
	if lb.Name != "a.example.com" || lb.Label != "demo" {
		t.Fatalf("get decoded wrong: %+v", lb)
	}
}
