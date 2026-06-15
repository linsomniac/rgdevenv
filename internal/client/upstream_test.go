package client

import "testing"

func TestParseUpstreamURL(t *testing.T) {
	ok := []struct {
		in     string
		scheme string
		host   string
		port   int
	}{
		{"http://localhost:9011", "http", "localhost", 9011},
		{"https://build-box:8443", "https", "build-box", 8443},
		{"http://10.0.0.5:80", "http", "10.0.0.5", 80},
	}
	for _, tc := range ok {
		s, h, p, err := ParseUpstreamURL(tc.in)
		if err != nil || s != tc.scheme || h != tc.host || p != tc.port {
			t.Fatalf("%s → (%s,%s,%d,%v), want (%s,%s,%d)", tc.in, s, h, p, err, tc.scheme, tc.host, tc.port)
		}
	}
	bad := []string{
		"localhost:9011",        // no scheme
		"ftp://host:21",         // bad scheme
		"http://host",           // no port
		"http://:9011",          // no host
		"http://host:9011/path", // path not allowed
		"http://user@host:9011", // userinfo not allowed
		"http://host:notaport",  // bad port
		"http://host:0",         // port 0 below range
		"http://host:99999",     // port above 65535
		"",                      // empty
	}
	for _, in := range bad {
		if _, _, _, err := ParseUpstreamURL(in); err == nil {
			t.Fatalf("%q should be rejected", in)
		}
	}
}
