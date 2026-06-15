package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds a Client pointed at srv with a fixed token.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{API: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDoSendsBearerAndDecodes(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	var out struct{ Hello string }
	if err := c.do(context.Background(), http.MethodGet, "/api/v1/thing", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if out.Hello != "world" {
		t.Fatalf("decoded = %+v", out)
	}
}

func TestDoMapsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "already exists", "code": "duplicate_lb"})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)

	err := c.do(context.Background(), http.MethodPost, "/api/v1/lbs", map[string]string{"name": "x"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if ae.Status != http.StatusConflict || ae.Code != "duplicate_lb" || !strings.Contains(ae.Message, "already exists") {
		t.Fatalf("APIError wrong: %+v", ae)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := New(Config{API: "://bad", Token: "t"}); err == nil {
		t.Fatal("expected error for bad base URL")
	}
}

func TestDoDiscardsBodyWhenOutNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	if err := c.do(context.Background(), http.MethodDelete, "/api/v1/lbs/x", nil, nil); err != nil {
		t.Fatalf("204 with nil out must succeed: %v", err)
	}
}

func TestDoNonJSONErrorFallsBackToStatusText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream is down"))
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	err := c.do(context.Background(), http.MethodGet, "/api/v1/status", nil, nil)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if ae.Status != http.StatusBadGateway || ae.Message != http.StatusText(http.StatusBadGateway) {
		t.Fatalf("non-JSON error must fall back to StatusText: %+v", ae)
	}
}
