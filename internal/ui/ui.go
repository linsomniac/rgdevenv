// Package ui serves the rgdevenv web dashboard: a data-free static bundle
// (index.html + app.css + app.js) embedded in the binary. It performs NO
// authentication — the bundle carries no data; the browser fetches everything
// from /api/v1/* with a bearer token kept only in sessionStorage (§14, §15).
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

//go:embed assets
var assets embed.FS

// contentSecurityPolicy is the strict CSP applied to every served asset (§15).
// It forbids inline scripts/styles, which is why app.css and app.js are separate
// embedded files. connect-src 'self' permits the dashboard's same-origin fetches
// to /api/v1/*.
//
// AIDEV-NOTE: font-src 'self' data: is permissive for fonts only. The page uses
// system fonts (no @font-face), but browser extensions (e.g. Dark Reader) inject
// data: fonts that would otherwise trip default-src 'none' and spam the console.
// Fonts are inert and style-src 'self' still gates any @font-face injection, so
// this does not weaken the script/connect/style protections.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self' data:; connect-src 'self'; img-src 'self' data:"

type file struct {
	body  []byte
	ctype string
}

// Handler returns the dashboard handler. "/" serves index.html; "/app.css" and
// "/app.js" serve those assets; every other path is 404 (no directory listing,
// no traversal — paths resolve by exact map lookup, not the filesystem).
//
// AIDEV-NOTE: panics here are startup programmer errors (a missing/renamed
// embedded asset), surfaced immediately rather than as a runtime 404.
// Handler() reads and content-types every embedded asset on each call, so it is
// meant to be called once at startup, not per request.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic("ui: embed sub: " + err.Error())
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		panic("ui: read embedded assets: " + err.Error())
	}
	files := make(map[string]file, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(sub, e.Name())
		if err != nil {
			panic("ui: read asset " + e.Name() + ": " + err.Error())
		}
		files["/"+e.Name()] = file{body: b, ctype: ctypeFor(e.Name())}
	}
	index, ok := files["/index.html"]
	if !ok {
		panic("ui: assets/index.html missing")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if r.URL.Path == "/" {
			serve(w, index)
			return
		}
		f, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serve(w, f)
	})
}

func serve(w http.ResponseWriter, f file) {
	w.Header().Set("Content-Type", f.ctype)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.body)
}

func ctypeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
