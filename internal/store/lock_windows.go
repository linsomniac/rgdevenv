//go:build windows

package store

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquireLock takes an exclusive, non-blocking lock on path using LockFileEx,
// the Windows analogue of flock. LOCKFILE_EXCLUSIVE_LOCK requests a write lock
// and LOCKFILE_FAIL_IMMEDIATELY makes it non-blocking, so a second instance
// fails immediately instead of waiting. Windows drops the lock when the handle
// closes (see Store.Close) or the process exits, matching the Unix lifecycle.
//
// AIDEV-NOTE: Windows half of the cross-platform instance lock; lock_unix.go is
// the flock sibling. Keep the semantics in sync: exclusive, non-blocking,
// released-on-close. Compiles in CI but is not runtime-tested on Windows here.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock %s: %w", path, err)
	}
	// Lock one byte at offset 0. The range is arbitrary but must be identical
	// across instances, which holds because every instance runs this same code.
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,    // reserved; must be zero
		1, 0, // bytes to lock: low word = 1, high word = 0
		new(windows.Overlapped),
	); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: another rgdevenv instance holds %s: %w", path, err)
	}
	return f, nil
}
