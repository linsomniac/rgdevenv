// Package proxy implements the data plane: TLS termination, routing, reverse
// proxying, the :80 redirect, listeners, and generic error pages.
package proxy

import (
	"io"
	"net/http"
)

// writeNotFound and writeBadGateway emit generic data-plane errors. They never
// reveal upstream identity (§8, §16); detail goes to logs only.
func writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, "404 Not Found\n")
}

func writeBadGateway(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	io.WriteString(w, "502 Bad Gateway\n")
}
