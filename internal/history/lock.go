package history

import (
	"fmt"
	"os"
	"path/filepath"

	"lobster/internal/config"
)

// withHistoryLock runs fn while holding an exclusive OS-level lock covering
// every history write.
//
// The lock lives in a side-car file rather than on the history file itself
// because each write replaces the history file by rename: a lock held on the
// old inode says nothing to a process that opens the new one.
//
// It is an OS lock rather than a mutex because the writers that actually
// collide are separate lobster processes — two `lobster play` runs open at
// once, each doing load, mutate, atomic rename — and a mutex serialises only
// one process's goroutines. It covers those as well: the lock is held per open
// file description, so two goroutines in one process block each other too.
//
// Readers are deliberately not locked. A write lands by rename, so a reader
// always sees one complete file, either the old one or the new one.
//
// Locking is best effort. If the lock file cannot be created or locked — a
// filesystem with no lock support, a data dir that is read-only — fn runs
// unlocked rather than failing: racing writers are better than never recording
// a watch, and unlocked is exactly what every write did before this existed.
func withHistoryLock(fn func() error) error {
	path, err := config.HistoryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating history dir: %w", err)
	}

	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fn()
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		return fn()
	}
	defer unlockFile(f)

	return fn()
}
