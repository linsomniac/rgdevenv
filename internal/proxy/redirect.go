package proxy

import (
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/canon"
)

// newRedirectHandler returns the :80 handler. It issues a 308 to the canonical
// https url ONLY for known, certificate-covered hosts; anything else gets a
// generic 404 (§6).
//
// AIDEV-NOTE: never echo an arbitrary Host into Location (open-redirect guard).
// Both certificate coverage AND a live route are required.
func newRedirectHandler(current func() *RoutingTable, resolver *CertResolver, httpsPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, err := canon.Host(r.Host)
		if err != nil || !resolver.Covers(host) || !current().HasHost(host) {
			writeNotFound(w)
			return
		}
		target := "https://" + host
		if httpsPort != 443 {
			target += ":" + strconv.Itoa(httpsPort)
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}
