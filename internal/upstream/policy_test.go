package upstream

import (
	"net"
	"testing"
)

func TestAllowedHost(t *testing.T) {
	p := NewPolicy([]string{"build-box", "10.0.0.0/8"})
	cases := []struct {
		host    string
		wantErr bool
	}{
		{"localhost", false},
		{"127.0.0.1", false},
		{"build-box", false},
		{"BUILD-BOX", false},
		{"10.1.2.3", false},
		{"evil.example.com", true},
		{"8.8.8.8", true},
		{"::1", false},            // IPv6 loopback allowed
		{"fe80::1", true},         // link-local literal not allowlisted
		{"169.254.169.254", true}, // metadata literal not allowlisted
	}
	for _, c := range cases {
		if err := p.AllowedHost(c.host); (err != nil) != c.wantErr {
			t.Errorf("AllowedHost(%q) err=%v wantErr=%v", c.host, err, c.wantErr)
		}
	}
}

func TestCheckDialIPDeniesMetadataAndLinkLocal(t *testing.T) {
	p := NewPolicy(nil)
	var self SelfGuard
	for _, ip := range []string{"169.254.169.254", "169.254.0.1", "fe80::1"} {
		if p.CheckDialIP(net.ParseIP(ip), 80, self) == "" {
			t.Errorf("expected %s to be denied", ip)
		}
	}
	if p.CheckDialIP(net.ParseIP("93.184.216.34"), 80, self) != "" {
		t.Error("public IP must not be denied by deny rules")
	}
}

func TestCheckDialIPSelfLoop(t *testing.T) {
	p := NewPolicy(nil)
	self := SelfGuard{ListenPorts: map[int]bool{443: true}}
	if p.CheckDialIP(net.ParseIP("127.0.0.1"), 443, self) == "" {
		t.Error("loopback on a listener port must be denied (loop)")
	}
	if p.CheckDialIP(net.ParseIP("127.0.0.1"), 9011, self) != "" {
		t.Error("loopback on a non-listener port (dev server) must be allowed")
	}
}

func TestCheckDialIPSelfLoopLocalIP(t *testing.T) {
	local := net.ParseIP("192.0.2.10")
	self := SelfGuard{LocalIPs: []net.IP{local}, ListenPorts: map[int]bool{443: true}}
	p := NewPolicy(nil)
	if p.CheckDialIP(local, 443, self) == "" {
		t.Error("local IP on a listener port must be denied (loop)")
	}
	if p.CheckDialIP(local, 9011, self) != "" {
		t.Error("local IP on a non-listener port must be allowed")
	}
}

func TestCheckDialIPNil(t *testing.T) {
	p := NewPolicy(nil)
	if p.CheckDialIP(nil, 80, SelfGuard{}) == "" {
		t.Error("nil IP must be denied")
	}
}
