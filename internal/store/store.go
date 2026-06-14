package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// Store owns the persisted state file, a single-instance lock, and the
// atomically published in-memory snapshot.
type Store struct {
	path     string
	lockFile *os.File
	snap     atomic.Pointer[State]
	mu       sync.Mutex // serializes Save
}

// Open acquires the instance lock, loads (or initializes) state, and publishes
// the first snapshot. Callers run store.Validate on the snapshot before serving.
func Open(path string) (*Store, error) {
	lf, err := acquireLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	st, err := loadState(path)
	if err != nil {
		lf.Close()
		return nil, err
	}
	s := &Store{path: path, lockFile: lf}
	s.snap.Store(st)
	return s, nil
}

// Snapshot returns the current immutable snapshot (lock-free read).
func (s *Store) Snapshot() *State { return s.snap.Load() }

// Publish atomically swaps in a new snapshot pointer.
func (s *Store) Publish(st *State) { s.snap.Store(st) }

// Close releases the instance lock.
func (s *Store) Close() error {
	if s.lockFile == nil {
		return nil
	}
	err := s.lockFile.Close()
	s.lockFile = nil
	return err
}

// Save writes st atomically and durably and is safe for concurrent callers.
// It persists to disk only and does NOT update the published snapshot; call
// Publish separately.
//
// AIDEV-NOTE: Durability ordering (§10): write temp → fsync temp → rename →
// fsync parent dir. CreateTemp yields 0600. Do not reorder; the parent-dir
// fsync is what makes the rename durable across a crash.
func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	// AIDEV-NOTE: A crash between CreateTemp and Rename can orphan a
	// .state-*.tmp file; it is harmless and ignored on the next start.
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("store: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		tmp.Close()
		return fmt.Errorf("store: encode: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("store: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("store: rename: %w", err)
	}
	return fsyncDir(dir)
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("store: open dir: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("store: fsync dir: %w", err)
	}
	return nil
}

// acquireLock takes an exclusive, non-blocking flock. flock associates the lock
// with the open file description, so a second Open (even in-process) fails.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: another rgdevenv instance holds %s: %w", path, err)
	}
	return f, nil
}

func loadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: CurrentVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var st State
	if err := dec.Decode(&st); err != nil {
		return nil, fmt.Errorf("store: parse %s: %w", path, err)
	}
	if st.Version > CurrentVersion {
		return nil, fmt.Errorf("store: state version %d is newer than supported %d", st.Version, CurrentVersion)
	}
	if st.Version == 0 {
		st.Version = CurrentVersion
	}
	return &st, nil
}
