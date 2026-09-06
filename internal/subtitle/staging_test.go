package subtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHome points os.UserHomeDir at a throwaway directory containing the
// standard Videos folder, so the staging-location tests never touch the real
// home directory.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "Videos"), 0o755); err != nil {
		t.Fatalf("creating fake Videos dir: %v", err)
	}
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	return home
}

// Snap-confined players (the VLC snap, for one) get a private /tmp and cannot
// see anything the host wrote there, so a staging directory under os.TempDir()
// makes every downloaded subtitle silently unloadable.
func TestNewTempDirIsNotUnderTempDir(t *testing.T) {
	home := fakeHome(t)

	td, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	t.Cleanup(td.Cleanup)

	// The fake home is itself under t.TempDir(), so "under os.TempDir()" cannot
	// be the assertion here; what must not happen is the staging dir being
	// created *directly in* the system temp dir, which is what os.MkdirTemp("")
	// does and what a confined player cannot see.
	if got := filepath.Dir(td.Path()); got == os.TempDir() {
		t.Errorf("staging dir %q was created directly in the system temp dir; a snap-confined player has its own private /tmp and cannot read it", td.Path())
	}
	if rel, err := filepath.Rel(home, td.Path()); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("staging dir %q is not under the home directory %q", td.Path(), home)
	}
}

// snapd's home interface exposes files under $HOME to a confined snap, but it
// excludes any path whose first component below $HOME is dot-prefixed. Verified
// empirically against the VLC snap: ~/.cache/... reads back "Permission
// denied", ~/Videos/.lobster/... loads fine.
func TestNewTempDirTopLevelHomeComponentIsNotHidden(t *testing.T) {
	home := fakeHome(t)

	td, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	t.Cleanup(td.Cleanup)

	rel, err := filepath.Rel(home, td.Path())
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("staging dir %q is not under home %q", td.Path(), home)
	}
	first := strings.Split(filepath.ToSlash(rel), "/")[0]
	if strings.HasPrefix(first, ".") {
		t.Errorf("staging dir %q sits under hidden top-level home directory %q; snapd's home interface hides those from confined players", td.Path(), first)
	}
}

func TestCleanupRemovesStagingDirAndEmptyParent(t *testing.T) {
	fakeHome(t)

	td, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	path := td.Path()
	if err := os.WriteFile(filepath.Join(path, "a.srt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing subtitle: %v", err)
	}

	td.Cleanup()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("staging dir %q still exists after Cleanup (stat err %v)", path, err)
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("empty parent %q still exists after Cleanup (stat err %v)", parent, err)
	}
}

// Home is not always usable (some containers have no writable $HOME). Playback
// must not fail over that — fall back to the old temp-dir behaviour.
func TestNewTempDirFallsBackWhenHomeUnusable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	t.Setenv("HOME", blocker)
	t.Setenv("USERPROFILE", blocker)

	td, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir must not fail when home is unusable: %v", err)
	}
	t.Cleanup(td.Cleanup)
	if fi, err := os.Stat(td.Path()); err != nil || !fi.IsDir() {
		t.Errorf("fallback staging dir %q unusable: err %v", td.Path(), err)
	}
}

// A hard kill skips Cleanup. /tmp self-cleans on reboot; a directory under
// $HOME does not, so stale staging dirs must be swept on the next run.
func TestNewTempDirPrunesStaleSiblings(t *testing.T) {
	fakeHome(t)

	first, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	stale := first.Path()
	if err := os.WriteFile(filepath.Join(stale, "old.srt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing subtitle: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("backdating staging dir: %v", err)
	}

	second, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir: %v", err)
	}
	t.Cleanup(second.Cleanup)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale staging dir %q survived a later NewTempDir (stat err %v)", stale, err)
	}
	if _, err := os.Stat(second.Path()); err != nil {
		t.Errorf("fresh staging dir %q was pruned: %v", second.Path(), err)
	}
}
