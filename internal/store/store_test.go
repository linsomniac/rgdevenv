package store

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStatePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state.json")
}

func TestOpenMissingStartsEmpty(t *testing.T) {
	s, err := Open(tempStatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Snapshot(); got.Version != CurrentVersion || len(got.LoadBalancers) != 0 {
		t.Fatalf("expected empty v%d state, got %+v", CurrentVersion, got)
	}
}

func TestSaveRoundTripAndMode(t *testing.T) {
	path := tempStatePath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		Version:       CurrentVersion,
		LoadBalancers: []LoadBalancer{{Name: "a.example.com"}},
	}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, want 0600", fi.Mode().Perm())
	}
	s.Close()

	// Reopen and confirm persistence.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Snapshot(); len(got.LoadBalancers) != 1 || got.LoadBalancers[0].Name != "a.example.com" {
		t.Fatalf("reopen lost data: %+v", got)
	}
}

func TestInstanceLock(t *testing.T) {
	path := tempStatePath(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("expected second Open to fail on instance lock")
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for newer version")
	}
}

func TestLoadRejectsMalformed(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{ not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for malformed state")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{"version":1,"bogus":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestLoadUpgradesVersionZero(t *testing.T) {
	path := tempStatePath(t)
	if err := os.WriteFile(path, []byte(`{"version":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Snapshot().Version != CurrentVersion {
		t.Fatalf("version 0 not upgraded to %d", CurrentVersion)
	}
}
