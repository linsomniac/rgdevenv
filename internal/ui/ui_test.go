package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestRootServesShell(t *testing.T) {
	w := get(t, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET / content-type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"<title>rgdevenv</title>", "/app.js", "/app.css"} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing %q", want)
		}
	}
}

func TestAssetContentTypes(t *testing.T) {
	for _, tc := range []struct{ path, ctype string }{
		{"/app.css", "text/css; charset=utf-8"},
		{"/app.js", "text/javascript; charset=utf-8"},
	} {
		w := get(t, tc.path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", tc.path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != tc.ctype {
			t.Fatalf("%s content-type = %q, want %q", tc.path, ct, tc.ctype)
		}
		if w.Body.Len() == 0 {
			t.Fatalf("%s body is empty", tc.path)
		}
	}
}

func TestCSPHeaderPresent(t *testing.T) {
	const want = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:"
	for _, p := range []string{"/", "/app.js", "/nope"} {
		if got := get(t, p).Header().Get("Content-Security-Policy"); got != want {
			t.Fatalf("CSP on %s = %q, want %q", p, got, want)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	for _, p := range []string{"/secret.txt", "/assets/index.html", "/../ui.go"} {
		if w := get(t, p); w.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", p, w.Code)
		}
	}
}

func TestShellIsDataFree(t *testing.T) {
	body := get(t, "/").Body.String()
	for _, forbidden := range []string{"Bearer", "Authorization", "load_balancers", "sessionStorage"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("shell unexpectedly contains %q — the served HTML must be data-free", forbidden)
		}
	}
}
