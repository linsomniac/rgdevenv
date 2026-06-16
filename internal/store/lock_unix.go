//go:build !windows

package store

import (
	"fmt"
	"os"
	"syscall"
)

// acquireLock takes an exclusive, non-blocking flock. flock associates the lock
// with the open file description, so a second Open (even in-process) fails. The
// lock is released when the returned file is closed (see Store.Close) or the
// process exits.
//
// AIDEV-NOTE: Unix half of the cross-platform instance lock; lock_windows.go is
// the LockFileEx sibling. Keep the semantics in sync: exclusive, non-blocking,
// released-on-close.
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
