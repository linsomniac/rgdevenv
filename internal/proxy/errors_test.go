package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	writeNotFound(w)
	if w.Code != 404 {
		t.Fatalf("code = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "404") {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestWriteBadGatewayNoLeak(t *testing.T) {
	w := httptest.NewRecorder()
	writeBadGateway(w)
	if w.Code != 502 {
		t.Fatalf("code = %d", w.Code)
	}
	body := strings.ToLower(w.Body.String())
	if strings.Contains(body, "localhost") || strings.Contains(body, "upstream") {
		t.Fatalf("502 body leaks detail: %q", body)
	}
}
