package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMappingMethods(t *testing.T) {
	var lastMethod, lastPath, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod, lastPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Mapping{ListenPort: 443, ListenTLS: true})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	ctx := context.Background()

	// Create with explicit upstream.
	port := 443
	tls := true
	req := MappingRequest{
		ListenPort: &port, ListenTLS: &tls,
		Upstream: &UpstreamRequest{Scheme: "http", Host: "localhost", Port: 9011, TLS: UpstreamTLSRequest{Mode: "verify"}},
	}
	if _, err := c.PutMapping(ctx, "a.example.com", req, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if lastMethod != http.MethodPost || lastPath != "/api/v1/lbs/a.example.com/mappings" {
		t.Fatalf("create request wrong: %s %s", lastMethod, lastPath)
	}
	if !strings.Contains(lastBody, `"host":"localhost"`) {
		t.Fatalf("create body missing upstream: %s", lastBody)
	}

	// Replace (PUT to /{port}).
	if _, err := c.PutMapping(ctx, "a.example.com", req, true); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if lastMethod != http.MethodPut || lastPath != "/api/v1/lbs/a.example.com/mappings/443" {
		t.Fatalf("replace request wrong: %s %s", lastMethod, lastPath)
	}

	// Delete.
	if err := c.DeleteMapping(ctx, "a.example.com", 443); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if lastMethod != http.MethodDelete || lastPath != "/api/v1/lbs/a.example.com/mappings/443" {
		t.Fatalf("delete request wrong: %s %s", lastMethod, lastPath)
	}
}

func TestPutMappingReplaceRequiresListenPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be called: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	if _, err := c.PutMapping(context.Background(), "a.example.com", MappingRequest{}, true); err == nil {
		t.Fatal("replace=true with nil ListenPort must error client-side")
	}
}
