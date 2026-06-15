package proxy

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

// Limits holds tunable server/transport timeouts and bounds (§8).
type Limits struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int

	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxRequestBody    int64 // 0 = unlimited (dev default)
}

// DefaultLimits returns safe defaults.
func DefaultLimits() Limits {
	return Limits{
		DialTimeout:           10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ReadHeaderTimeout:     10 * time.Second,
		IdleTimeout:           120 * time.Second,
		MaxHeaderBytes:        1 << 20,
		MaxRequestBody:        0,
	}
}

// BuildReverseProxy builds an httputil.ReverseProxy for one upstream using the
// shared safe dialer and the upstream's TLS mode (§7, §8). listenTLS is the
// front-end mapping's TLS state (used for X-Forwarded-Proto). onError, if
// non-nil, is called each time the ErrorHandler fires (live-failure feed, §17).
func BuildReverseProxy(up store.Upstream, listenTLS bool, dialer *upstream.Dialer, caDir string, limits Limits, logger *slog.Logger, onError func()) (*httputil.ReverseProxy, error) {
	if up.Scheme != "http" && up.Scheme != "https" {
		return nil, fmt.Errorf("proxy: invalid upstream scheme %q", up.Scheme)
	}
	target := &url.URL{Scheme: up.Scheme, Host: net.JoinHostPort(up.Host, strconv.Itoa(up.Port))}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          limits.MaxIdleConns,
		MaxIdleConnsPerHost:   limits.MaxIdleConnsPerHost,
		IdleConnTimeout:       limits.IdleConnTimeout,
		TLSHandshakeTimeout:   limits.TLSHandshakeTimeout,
		ResponseHeaderTimeout: limits.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if up.Scheme == "https" {
		tlsCfg, err := upstreamTLSConfig(up, caDir)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			setForwardedHeaders(pr, listenTLS)
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// AIDEV-NOTE: a canceled request context means the CLIENT went away (or the
			// server is shutting down), NOT an upstream failure — don't log it as an
			// upstream error or feed it to the health checker (§17). Without this guard,
			// normal client cancels would flap a healthy upstream to "down" once
			// Threshold consecutive cancels land between probe rounds.
			if r.Context().Err() != nil {
				return
			}
			// full detail to logs; client gets a generic 502 (§8, §16).
			logger.Warn("upstream error", "host", r.Host, "upstream", target.String(), "error", err)
			if onError != nil {
				onError()
			}
			writeBadGateway(w)
		},
	}
	return rp, nil
}

// setForwardedHeaders strips client-supplied forwarding/identity headers and sets
// rgdevenv's own (§8). With ReverseProxy.Rewrite these are NOT added
// automatically, so rgdevenv is the sole authority and spoofing is impossible.
func setForwardedHeaders(pr *httputil.ProxyRequest, listenTLS bool) {
	out, in := pr.Out, pr.In
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP", "Forwarded"} {
		out.Header.Del(h)
	}
	clientIP, _, _ := net.SplitHostPort(in.RemoteAddr)
	out.Header.Set("X-Forwarded-For", clientIP)
	out.Header.Set("X-Real-IP", clientIP)
	proto := "http"
	if listenTLS {
		proto = "https"
	}
	out.Header.Set("X-Forwarded-Proto", proto)
	out.Header.Set("X-Forwarded-Host", in.Host)
}

func upstreamTLSConfig(up store.Upstream, caDir string) (*tls.Config, error) {
	return upstream.TLSClientConfig(up.TLS.Mode, up.TLS.CAName, up.Host, caDir)
}
