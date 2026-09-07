package history

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"lobster/internal/config"
)

var (
	logMu sync.RWMutex
	logf  func(string, ...any)
)

// SetLogger wires a sink for the one diagnostic this package emits: the notice
// that a history write ran without the lock. Nil (the default) keeps the
// package silent, so packages that never wire one are unaffected.
func SetLogger(fn func(string, ...any)) {
	logMu.Lock()
	defer logMu.Unlock()
	logf = fn
}

// warnUnlocked reports that locking degraded and the write is going ahead
// anyway. It exists so the fallback is diagnosable: the failure mode it
// precedes — a row silently lost to a concurrent lobster — is otherwise
// invisible from the outside.
func warnUnlocked(stage string, err error) {
	logMu.RLock()
	fn := logf
	logMu.RUnlock()
	if fn == nil {
		return
	}
	fn("history lock unavailable (%s: %v); writing unlocked, so a lobster writing at the same moment can drop a row", stage, err)
}

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
// Locking is best effort, and the trade-off is real in both directions. When
// the lock file cannot be created or locked — a filesystem with no working
// flock, such as some NFS and FUSE mounts — fn runs unlocked, which means the
// load-modify-rename race this lock exists to close is back and a concurrent
// writer's row can be lost. Failing the write instead would trade that
// unlikely lost row for recording no history at all on those filesystems,
// which is the worse outcome for the user; unlocked is also exactly what every
// write did before this lock existed, so nothing regresses. The fallback is
// announced through warnUnlocked rather than taken silently.
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
		warnUnlocked("opening lock file", err)
		return fn()
	}
	defer f.Close()

	if err := lockFile(f); err != nil {
		warnUnlocked("taking lock", err)
		return fn()
	}
	defer unlockFile(f)

	return fn()
}
