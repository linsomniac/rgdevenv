package canon

import "testing"

func TestHost(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercase", "Example.COM", "example.com", false},
		{"trailing dot", "host.example.com.", "host.example.com", false},
		{"strip port", "host.example.com:8443", "host.example.com", false},
		{"mixed case + dot + port", "RG-27788-CpCart.Sean.Realgo.com.:443", "rg-27788-cpcart.sean.realgo.com", false},
		{"idna", "Bücher.example", "xn--bcher-kva.example", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"space inside", "bad host", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Host(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Host(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Host(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Host(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
