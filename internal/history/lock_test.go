package history

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
)

// captureLockWarnings wires SetLogger to a slice for the duration of the test
// and restores the previous sink afterwards, so one test's logger never leaks
// into another's.
func captureLockWarnings(t *testing.T) func() []string {
	t.Helper()

	var mu sync.Mutex
	var got []string
	SetLogger(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, format)
	})
	t.Cleanup(func() { SetLogger(nil) })

	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// blockLockFile makes the side-car lock path unopenable by occupying it with a
// directory, which is the closest reproducible stand-in for the filesystems
// (some NFS, some FUSE) where lock setup genuinely fails.
func blockLockFile(t *testing.T) {
	t.Helper()

	path, err := config.HistoryPath()
	if err != nil {
		t.Fatalf("HistoryPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("creating data dir: %v", err)
	}
	if err := os.Mkdir(path+".lock", 0700); err != nil {
		t.Fatalf("blocking lock path: %v", err)
	}
}

// TestSaveStillRecordsWhenLockSetupFails pins the fail-open half of the
// trade-off: on a filesystem where the lock cannot be established, a watch is
// still recorded rather than lost. Failing the write instead would leave such
// users with no history at all.
func TestSaveStillRecordsWhenLockSetupFails(t *testing.T) {
	setDataHome(t, t.TempDir())
	blockLockFile(t)
	captureLockWarnings(t)

	if err := Save(media.HistoryEntry{ID: "movie/1", Title: "One", Type: media.Movie}); err != nil {
		t.Fatalf("Save with lock setup failing: %v, want the write to proceed unlocked", err)
	}

	entries, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "movie/1" {
		t.Fatalf("history holds %+v, want the one row written while locking was unavailable", entries)
	}
}

// TestSaveAnnouncesAnUnlockedWrite pins the other half: the downgrade is not
// silent. Without a diagnostic, a user on such a filesystem has no way to tell
// that their history writes lost the guarantee this lock provides.
func TestSaveAnnouncesAnUnlockedWrite(t *testing.T) {
	setDataHome(t, t.TempDir())
	blockLockFile(t)
	warnings := captureLockWarnings(t)

	if err := Save(media.HistoryEntry{ID: "movie/1", Title: "One", Type: media.Movie}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := warnings()
	if len(got) != 1 {
		t.Fatalf("got %d lock warnings (%q), want exactly 1: an unlocked history write must be announced, not taken silently", len(got), got)
	}
	if !strings.Contains(got[0], "unlocked") {
		t.Errorf("warning %q does not say the write went ahead unlocked", got[0])
	}
}

// TestSaveIsSilentWhenLockingWorks keeps the diagnostic honest: it must fire
// on the degraded path only, or it stops meaning anything.
func TestSaveIsSilentWhenLockingWorks(t *testing.T) {
	setDataHome(t, t.TempDir())
	warnings := captureLockWarnings(t)

	if err := Save(media.HistoryEntry{ID: "movie/1", Title: "One", Type: media.Movie}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := warnings(); len(got) != 0 {
		t.Fatalf("got lock warnings %q on a working filesystem, want none", got)
	}
}
