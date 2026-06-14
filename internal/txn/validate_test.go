package txn

import (
	"errors"
	"strings"
	"testing"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func baseParams() ValidateParams {
	return ValidateParams{
		Policy:       upstream.NewPolicy([]string{"build-box"}),
		Covers:       func(h string) bool { return strings.HasSuffix(h, ".sean.realgo.com") },
		PoolStart:    9000,
		PoolEnd:      9999,
		HTTPSPort:    443,
		HTTPPort:     80,
		MgmtBindPort: 0,
		MgmtHost:     "rgdevenv.sean.realgo.com",
		CADir:        "/nonexistent",
	}
}

func validLB() store.LoadBalancer {
	return store.LoadBalancer{
		Name: "rg-1.sean.realgo.com",
		Mappings: []store.Mapping{{
			ListenPort: 443, ListenTLS: true,
			Upstream: store.Upstream{Scheme: "http", Host: "localhost", Port: 9011, TLS: store.UpstreamTLS{Mode: "verify"}},
		}},
	}
}

func stateWith(lb store.LoadBalancer) *store.State {
	return &store.State{Version: store.CurrentVersion, LoadBalancers: []store.LoadBalancer{lb}}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(stateWith(validLB()), baseParams()); err != nil {
		t.Fatalf("valid state rejected: %v", err)
	}
}

func TestValidateListenPortInPool(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 9500
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

func TestValidateReservedHTTPPort(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 80
	lb.Mappings[0].ListenTLS = false
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for redirect port, got %v", err)
	}
}

func TestValidateHTTPSPortRequiresTLS(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenTLS = false
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for non-TLS on https port, got %v", err)
	}
}

func TestValidateUpstreamNotAllowed(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Host = "evil.example.com"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestValidateBadScheme(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Scheme = "ftp"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestValidateCANotFound(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Scheme = "https"
	lb.Mappings[0].Upstream.TLS = store.UpstreamTLS{Mode: "ca", CAName: "missing"}
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for missing CA, got %v", err)
	}
}

func TestValidateHostNotCovered(t *testing.T) {
	lb := validLB()
	lb.Name = "rg-1.other.com"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for uncovered host, got %v", err)
	}
}

func TestValidateReservedName(t *testing.T) {
	lb := validLB()
	lb.Name = "rgdevenv.sean.realgo.com"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for reserved name, got %v", err)
	}
}

func TestValidateNonCanonicalName(t *testing.T) {
	lb := validLB()
	lb.Name = "RG-1.Sean.Realgo.COM"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for non-canonical name, got %v", err)
	}
}

func TestValidateErrorCarriesCode(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 9500
	var te *Error
	if !errors.As(Validate(stateWith(lb), baseParams()), &te) || te.Code == "" {
		t.Fatal("expected a *txn.Error with a machine code")
	}
}

func TestValidateInvalidListenPort(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 0
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for listen_port 0, got %v", err)
	}
}

func TestValidateInvalidUpstreamPort(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.Port = 0
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for upstream port 0, got %v", err)
	}
}

func TestValidateInvalidTLSMode(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].Upstream.TLS.Mode = "bogus"
	if err := Validate(stateWith(lb), baseParams()); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation for bad tls mode, got %v", err)
	}
}

func TestValidateMgmtBindPortConflict(t *testing.T) {
	lb := validLB()
	lb.Mappings[0].ListenPort = 8443
	p := baseParams()
	p.MgmtBindPort = 8443
	if err := Validate(stateWith(lb), p); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for mgmt bind port, got %v", err)
	}
}
