package upstream

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeConn is a stand-in net.Conn; its methods are never called in these tests.
type fakeConn struct{ net.Conn }

func TestDialerPinsValidatedIP(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	var dialed string
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5")}, nil
	}
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "build-box:8443"); err != nil {
		t.Fatal(err)
	}
	// AIDEV-NOTE: dialing the IP (not the host) proves no OS re-resolution.
	if dialed != "203.0.113.5:8443" {
		t.Fatalf("expected to dial the pinned IP, got %q", dialed)
	}
}

func TestDialerDeniesIfAnyAddressDenied(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5"), net.ParseIP("169.254.169.254")}, nil
	}
	called := false
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return fakeConn{}, nil
	}
	_, err := d.DialContext(context.Background(), "tcp", "build-box:8443")
	if err == nil {
		t.Fatal("expected deny-if-any error")
	}
	if called {
		t.Fatal("must not dial when any address is denied")
	}
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PolicyError, got %T", err)
	}
}

func TestDialerRejectsNonAllowlistedBeforeResolve(t *testing.T) {
	d := New(NewPolicy(nil), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		t.Fatal("resolve must not be called for a non-allowlisted host")
		return nil, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "evil.example.com:80"); err == nil {
		t.Fatal("expected policy rejection")
	}
}

func TestDialerAllowsLocalhostDevServer(t *testing.T) {
	self := SelfGuard{ListenPorts: map[int]bool{443: true}} // 443 is ours; 9011 is not
	d := New(NewPolicy(nil), self, 0)
	var dialed string
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = addr
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "localhost:9011"); err != nil {
		t.Fatal(err)
	}
	if dialed != "127.0.0.1:9011" {
		t.Fatalf("dialed %q", dialed)
	}
}

func TestDialerTriesNextIPOnDialFailure(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.5"), net.ParseIP("203.0.113.6")}, nil
	}
	var dialed []string
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if addr == "203.0.113.5:8443" {
			return nil, errors.New("connection refused")
		}
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "build-box:8443"); err != nil {
		t.Fatal(err)
	}
	if len(dialed) != 2 || dialed[1] != "203.0.113.6:8443" {
		t.Fatalf("expected fallback to the second validated IP, dialed=%v", dialed)
	}
}

func TestDialerResolveError(t *testing.T) {
	d := New(NewPolicy([]string{"build-box"}), SelfGuard{}, 0)
	d.Resolve = func(ctx context.Context, host string) ([]net.IP, error) {
		return nil, errors.New("nxdomain")
	}
	called := false
	d.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return fakeConn{}, nil
	}
	if _, err := d.DialContext(context.Background(), "tcp", "build-box:8443"); err == nil {
		t.Fatal("expected resolve error")
	}
	if called {
		t.Fatal("must not dial on resolve error")
	}
}
