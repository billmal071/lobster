//go:build windows

package history

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive byte-range lock on f, blocking until it is
// granted. Windows drops the lock when the handle closes, including on process
// exit, so a lobster killed mid-write leaves no stale lock behind.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, new(windows.Overlapped))
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
