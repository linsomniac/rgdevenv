package api

import (
	"net"
	"net/http"

	"github.com/realgo/rgdevenv/internal/auth"
)

// authMiddleware enforces bearer auth + per-IP failed-attempt rate limiting (§15).
//
// AIDEV-NOTE: bearer-only — cookies are never consulted, so there is no CSRF
// surface. Rate-limit check precedes auth so a flood of bad tokens is capped.
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !h.limiter.Allowed(ip) {
			writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many failed attempts", Code: "rate_limited"})
			return
		}
		tok, ok := auth.ParseBearer(r.Header.Get("Authorization"))
		if !ok || !h.auth.Check(tok) {
			h.limiter.RecordFailure(ip)
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized", Code: "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// audit logs a management mutation (actor = client IP) (§17).
func (h *Handler) audit(r *http.Request, action, target string) {
	h.logger.Info("audit", "actor", clientIP(r), "action", action, "target", target)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
