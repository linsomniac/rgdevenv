package upstream

import "testing"

func TestTLSClientConfigModes(t *testing.T) {
	if c, err := TLSClientConfig("verify", "", "host", ""); err != nil || c.InsecureSkipVerify {
		t.Fatalf("verify: c=%+v err=%v", c, err)
	}
	if c, err := TLSClientConfig("", "", "host", ""); err != nil || c.InsecureSkipVerify {
		t.Fatalf("empty mode == verify: c=%+v err=%v", c, err)
	}
	if c, err := TLSClientConfig("skip", "", "host", ""); err != nil || !c.InsecureSkipVerify {
		t.Fatalf("skip: c=%+v err=%v", c, err)
	}
	if _, err := TLSClientConfig("bogus", "", "host", ""); err == nil {
		t.Fatal("unknown mode must error")
	}
	if _, err := TLSClientConfig("ca", "missing", "host", t.TempDir()); err == nil {
		t.Fatal("ca mode with missing CA must error")
	}
}

func TestTLSClientConfigServerName(t *testing.T) {
	c, err := TLSClientConfig("verify", "", "up.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerName != "up.example" {
		t.Fatalf("ServerName = %q, want up.example", c.ServerName)
	}
}
