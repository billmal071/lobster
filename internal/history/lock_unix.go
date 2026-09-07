//go:build !windows

package history

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f, blocking until it is granted. flock
// is released by the kernel when the file description closes, including when
// the process dies, so a lobster killed mid-write leaves no stale lock behind.
func lockFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		// A signal can interrupt the blocking wait; that is not a failure to
		// lock, so try again rather than falling back to an unlocked write.
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
