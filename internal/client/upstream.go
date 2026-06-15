package client

import (
	"fmt"
	"net/url"
	"strconv"
)

// ParseUpstreamURL parses an --upstream value like "http://localhost:9011" or
// "https://build-box:8443" into scheme/host/port. Scheme must be http or
// https; host and an explicit port are required; path/query/userinfo are rejected.
//
// AIDEV-NOTE: url.Parse is permissive — it accepts "localhost:9011" as scheme="localhost".
// The scheme guard catches this. Non-numeric ports for known schemes cause url.Parse
// itself to return an error, caught by the first guard.
func ParseUpstreamURL(raw string) (scheme, host string, port int, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", 0, fmt.Errorf("client: invalid upstream URL %q: %w", raw, perr)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", 0, fmt.Errorf("client: upstream scheme must be http or https: %q", raw)
	}
	if u.User != nil {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain userinfo: %q", raw)
	}
	if p := u.EscapedPath(); p != "" && p != "/" {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain a path: %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", 0, fmt.Errorf("client: upstream URL must not contain a query or fragment: %q", raw)
	}
	h, portStr := u.Hostname(), u.Port()
	if h == "" {
		return "", "", 0, fmt.Errorf("client: upstream URL missing host: %q", raw)
	}
	if portStr == "" {
		return "", "", 0, fmt.Errorf("client: upstream URL must include an explicit port: %q", raw)
	}
	pn, cerr := strconv.Atoi(portStr)
	if cerr != nil || pn < 1 || pn > 65535 {
		return "", "", 0, fmt.Errorf("client: invalid upstream port in %q", raw)
	}
	return u.Scheme, h, pn, nil
}
