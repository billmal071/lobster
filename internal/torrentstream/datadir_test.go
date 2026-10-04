package torrentstream

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lobster/internal/userdir"
)

// fakeHome points os.UserHomeDir at a throwaway directory containing the
// standard Videos folder, reached through a symlink so the resolved and
// unresolved homes differ exactly as they do on macOS.
func fakeHome(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "Videos"), 0o755); err != nil {
		t.Fatalf("creating fake Videos dir: %v", err)
	}
	home := filepath.Join(base, "home")
	if err := os.Symlink(real, home); err != nil {
		home = real // Windows without developer mode, say.
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// resolvedRel relativises path against home with every symlink resolved.
func resolvedRel(t *testing.T, home, path string) string {
	t.Helper()
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolving home %q: %v", home, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %q: %v", path, err)
	}
	rel, err := filepath.Rel(resolvedHome, resolved)
	if err != nil {
		t.Fatalf("relativising %q against %q: %v", resolved, resolvedHome, err)
	}
	return rel
}

// A torrent payload is the largest thing lobster writes — a 4K remux runs to
// tens of gigabytes — so the default must land on the user's home volume, not
// on whatever small filesystem backs os.TempDir(). (The same path is invisible
// to a snap-confined player, which gets a private /tmp.)
func TestNewDataDirDefaultsUnderHome(t *testing.T) {
	home := fakeHome(t)

	data, err := newDataDir("")
	if err != nil {
		t.Fatalf("newDataDir(\"\"): %v", err)
	}
	dir := data.path
	t.Cleanup(data.remove)

	if rel := resolvedRel(t, home, dir); strings.HasPrefix(rel, "..") {
		t.Errorf("torrent data dir %q resolves outside the home directory (rel %q); a multi-gigabyte payload must not land on the root filesystem", dir, rel)
	}
	// The fake home is itself under t.TempDir(), so "under os.TempDir()"
	// cannot be the assertion; what must not happen is the data dir being
	// created *directly in* the system temp dir, which is what
	// os.MkdirTemp("", ...) does and what fills the root filesystem.
	tmp, err := filepath.EvalSymlinks(filepath.Clean(os.TempDir()))
	if err != nil {
		t.Fatalf("resolving the system temp dir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %q: %v", dir, err)
	}
	if filepath.Dir(resolved) == tmp {
		t.Errorf("torrent data dir %q was created directly in the system temp dir", dir)
	}
}

// A configured directory is a place to put data in, not a directory to write
// into directly: the payload goes in a fresh per-run subdirectory so the run
// can delete its own data without touching anything else the user keeps there.
func TestNewDataDirUsesAFreshSubdirOfTheConfiguredDir(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "torrents")
	keep := filepath.Join(configured, "keepme")
	if err := os.MkdirAll(keep, 0o700); err != nil {
		t.Fatalf("seeding configured dir: %v", err)
	}

	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	dir := data.path
	t.Cleanup(data.remove)

	if parent := filepath.Dir(dir); parent != configured {
		t.Errorf("data dir %q is not directly inside the configured dir %q", dir, configured)
	}
	if dir == configured {
		t.Errorf("data dir %q is the configured dir itself; a per-run subdirectory is what makes cleanup safe", dir)
	}
	if !strings.HasPrefix(filepath.Base(dir), dataPrefix+"-") {
		t.Errorf("data dir %q is not named %q-<random>; the prune sweep only recognises that prefix", dir, dataPrefix)
	}

	secondData, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("second newDataDir(%q): %v", configured, err)
	}
	second := secondData.path
	t.Cleanup(secondData.remove)
	if second == dir {
		t.Errorf("two runs got the same data dir %q; concurrent runs would corrupt each other's pieces", dir)
	}
}

// Nothing reclaims a directory under $HOME, so a run killed before Close
// leaves its partial payload there forever. A later run sweeps it.
func TestNewDataDirPrunesAbandonedSiblings(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "torrents")
	stale := filepath.Join(configured, dataPrefix+"-stale")
	unrelated := filepath.Join(configured, "keepme")
	for _, d := range []string{stale, unrelated} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("seeding %q: %v", d, err)
		}
		// Both are marked as lobster's, so the prefix is the only thing that
		// separates them here — the marker is covered by the lookalike test.
		if err := userdir.MarkOwned(d); err != nil {
			t.Fatalf("marking %q: %v", d, err)
		}
		old := time.Now().Add(-dataStaleAfter - time.Hour)
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatalf("backdating %q: %v", d, err)
		}
	}

	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	dir := data.path
	t.Cleanup(data.remove)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("abandoned data dir %q survived a later run (stat err %v); torrent payloads would accumulate unbounded", stale, err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated directory %q was pruned: %v", unrelated, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("this run's data dir %q was pruned: %v", dir, err)
	}
}

// Close owns the payload: a persistent default would otherwise grow by tens of
// gigabytes per film with nothing deleting it.
func TestCloseRemovesTheDataDirButNotItsParent(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "torrents")
	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	dir := data.path
	if err := os.WriteFile(filepath.Join(dir, "piece.bin"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing payload: %v", err)
	}

	s := &Server{data: data}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("data dir %q survived Close (stat err %v)", dir, err)
	}
	if _, err := os.Stat(configured); err != nil {
		t.Errorf("configured parent %q was removed by Close: %v", configured, err)
	}
}

// A configured torrent_dir is a directory the user chose, so it can hold
// directories lobster never created — and "starts with torrent-" is a name a
// user is entitled to use. Pruning by name alone deletes their data: the sweep
// is recursive and the window is only 24 hours.
func TestNewDataDirKeepsUnownedLookalikeDirectories(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "Downloads")
	theirs := filepath.Join(configured, dataPrefix+"-backups")
	theirFile := filepath.Join(theirs, "2019-tax-return.pdf")
	if err := os.MkdirAll(theirs, 0o700); err != nil {
		t.Fatalf("seeding %q: %v", theirs, err)
	}
	if err := os.WriteFile(theirFile, []byte("not lobster's"), 0o600); err != nil {
		t.Fatalf("seeding %q: %v", theirFile, err)
	}
	old := time.Now().Add(-dataStaleAfter - time.Hour)
	if err := os.Chtimes(theirs, old, old); err != nil {
		t.Fatalf("backdating %q: %v", theirs, err)
	}

	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	t.Cleanup(data.remove)

	if _, err := os.Stat(theirFile); err != nil {
		t.Errorf("a run deleted %q, a file lobster never created, because its parent directory's name begins with %q-: %v", theirFile, dataPrefix, err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("a run deleted the user's own directory %q: %v", theirs, err)
	}
}

// Pruning by marker only works if lobster marks what it creates: the marker is
// what bounds growth in the configured case, where a run killed before Close
// leaves tens of gigabytes behind and nothing else reclaims it.
func TestNewDataDirSweepsItsOwnAbandonedRun(t *testing.T) {
	configured := filepath.Join(t.TempDir(), "torrents")

	abandonedData, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	abandoned := abandonedData.path
	if err := os.WriteFile(filepath.Join(abandoned, "piece.bin"), []byte("x"), 0o600); err != nil {
		t.Fatalf("writing payload: %v", err)
	}
	old := time.Now().Add(-dataStaleAfter - time.Hour)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatalf("backdating %q: %v", abandoned, err)
	}

	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("second newDataDir(%q): %v", configured, err)
	}
	t.Cleanup(data.remove)

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("a data dir this code created itself survived a later run (stat err %v); payloads would accumulate unbounded in the configured dir", err)
	}
}

// userdir.Remove tidies the shared parent away once the last run directory
// leaves it, and recognises that parent by its name. A user is free to point
// torrent_dir at a directory of their own called ".lobster" — at which point
// the same cleanup deletes the directory they configured, along with whatever
// permissions they set on it. A configured directory is never lobster's to
// remove.
func TestCloseKeepsAConfiguredParentCalledDotLobster(t *testing.T) {
	configured := filepath.Join(t.TempDir(), userdir.Parent)
	if err := os.MkdirAll(configured, 0o750); err != nil {
		t.Fatalf("creating configured dir: %v", err)
	}
	before, err := os.Stat(configured)
	if err != nil {
		t.Fatalf("stat configured dir: %v", err)
	}

	data, err := newDataDir(configured)
	if err != nil {
		t.Fatalf("newDataDir(%q): %v", configured, err)
	}
	dir := data.path

	s := &Server{data: data}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("data dir %q survived Close (stat err %v)", dir, err)
	}
	after, err := os.Stat(configured)
	if err != nil {
		t.Fatalf("Close removed the configured torrent_dir %q because it is named %q: %v", configured, userdir.Parent, err)
	}
	if runtime.GOOS != "windows" && after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("configured dir %q came back with mode %v, was %v", configured, after.Mode().Perm(), before.Mode().Perm())
	}
}

// The other direction: lobster's own .lobster parent under $HOME is lobster's
// to tidy, and must still be removed once the last run leaves it. Sparing every
// parent would leave an empty dot-directory in the user's Videos folder forever.
func TestCloseTidiesTheDefaultParent(t *testing.T) {
	fakeHome(t)

	data, err := newDataDir("")
	if err != nil {
		t.Fatalf("newDataDir(\"\"): %v", err)
	}
	dir := data.path
	// The temp-dir fallback has no .lobster parent to tidy, so there would be
	// nothing to assert; it only happens when no base under $HOME is usable.
	parent := filepath.Dir(dir)
	if filepath.Base(parent) != userdir.Parent {
		t.Skipf("default landed in %q, whose parent is not %q; no home base was usable", dir, userdir.Parent)
	}

	s := &Server{data: data}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("lobster's own parent %q survived removal of the last run directory in it (stat err %v)", parent, err)
	}
}
