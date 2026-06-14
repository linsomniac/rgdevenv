package upstream

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var caNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidCAName reports whether name is a path-safe CA identifier (§7): no path
// separators, no "..", conservative charset. The on-disk file is
// <ca_dir>/<name>.pem.
func ValidCAName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return caNameRe.MatchString(name)
}

// ListCAs returns the sorted names of *.pem CA files in caDir (extension
// stripped). A missing directory yields nil with no error (§12 GET /cas).
func ListCAs(caDir string) ([]string, error) {
	entries, err := os.ReadDir(caDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if n := e.Name(); strings.HasSuffix(n, ".pem") {
			names = append(names, strings.TrimSuffix(n, ".pem"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadCA loads the named private CA into a fresh pool that trusts ONLY this CA
// (system roots are not included) — used for upstream tls mode "ca" (§7).
func LoadCA(caDir, name string) (*x509.CertPool, error) {
	if !ValidCAName(name) {
		return nil, fmt.Errorf("upstream: invalid ca_name %q", name)
	}
	path := filepath.Join(caDir, name+".pem")
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("upstream: read CA %q: %w", name, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("upstream: no certificates in CA %q", name)
	}
	return pool, nil
}
