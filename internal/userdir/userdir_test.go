package userdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHome points os.UserHomeDir at a throwaway directory containing the
// standard Videos folder, so these tests never touch the real home directory.
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

// The whole point of the package: a directory a confined helper can see is
// under $HOME, not in the system temp dir it gets a private copy of.
func TestMakeIsUnderHomeAndNotDirectlyInTempDir(t *testing.T) {
	home := fakeHome(t)

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if !visible {
		t.Errorf("Make reported the directory invisible to a confined helper, but a usable home was available")
	}
	// The fake home is itself under t.TempDir(), so "under os.TempDir()" cannot
	// be the assertion; what must not happen is the directory being created
	// *directly in* the system temp dir, which is what os.MkdirTemp("") does.
	if got := filepath.Dir(dir); got == os.TempDir() {
		t.Errorf("Make created %q directly in the system temp dir; a confined helper has its own private /tmp and cannot reach it", dir)
	}
	if rel, err := filepath.Rel(home, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("Make created %q outside the home directory %q", dir, home)
	}
}

// snapd's home interface excludes any path whose FIRST component below $HOME is
// dot-prefixed, so ".lobster" may only ever be nested inside a visible
// directory — never the top-level one.
func TestMakeTopLevelHomeComponentIsNotHidden(t *testing.T) {
	home := fakeHome(t)

	dir, _, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	rel, err := filepath.Rel(home, dir)
	if err != nil {
		t.Fatalf("relativising %q against %q: %v", dir, home, err)
	}
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	if strings.HasPrefix(top, ".") {
		t.Errorf("top-level component %q of %q is hidden; snapd denies a confined helper any path below $HOME whose first component is dot-prefixed", top, rel)
	}
}

// A caller that cannot use any home-based directory still gets somewhere to
// work, and is told the handoff is not sandbox-safe rather than left to guess.
func TestMakeFallsBackWhenEveryHomeCandidateIsVetoed(t *testing.T) {
	home := fakeHome(t)

	dir, visible, err := Make("probe", time.Hour, func(candidate string) bool {
		rel, relErr := filepath.Rel(home, candidate)
		return relErr != nil || strings.HasPrefix(rel, "..")
	})
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if visible {
		t.Errorf("Make claimed %q is visible to a confined helper, but every home candidate was vetoed", dir)
	}
	if filepath.Dir(dir) != os.TempDir() {
		t.Errorf("fallback directory %q is not in the system temp dir %q", dir, os.TempDir())
	}
	// A vetoed candidate must not be left lying around under $HOME.
	if entries, err := os.ReadDir(filepath.Join(home, "Videos", Parent)); err == nil && len(entries) != 0 {
		t.Errorf("vetoed candidates were left behind under %s: %v", Parent, entries)
	}
}

// With no home at all — a container running as a user with no passwd entry —
// there is still somewhere to work, flagged as not sandbox-visible.
func TestMakeFallsBackWhenHomeUnusable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if visible {
		t.Errorf("Make claimed %q is sandbox-visible with no home directory available", dir)
	}
	if filepath.Dir(dir) != os.TempDir() {
		t.Errorf("fallback directory %q is not in the system temp dir %q", dir, os.TempDir())
	}
}

// A run killed before it could clean up leaves a directory that nothing else
// reclaims, so a later run sweeps it — but only its own kind. Two callers share
// the .lobster parent and must not delete each other's live directories.
func TestPruneStaleSweepsOnlyItsOwnStalePrefix(t *testing.T) {
	parent := t.TempDir()
	stale := time.Now().Add(-48 * time.Hour)

	mine := filepath.Join(parent, "probe-stale")
	theirs := filepath.Join(parent, "other-stale")
	fresh := filepath.Join(parent, "probe-fresh")
	for _, d := range []string{mine, theirs, fresh} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}
	for _, d := range []string{mine, theirs} {
		if err := os.Chtimes(d, stale, stale); err != nil {
			t.Fatalf("ageing %s: %v", d, err)
		}
	}

	PruneStale(parent, "probe", 24*time.Hour)

	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Errorf("a stale probe- directory survived the sweep (err=%v)", err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("the sweep deleted another caller's directory: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("the sweep deleted a directory younger than staleAfter: %v", err)
	}
}

// Remove tidies the shared parent away once it empties, but never while another
// run still has a directory in it.
func TestRemoveClearsEmptyParentOnly(t *testing.T) {
	home := fakeHome(t)
	parent := filepath.Join(home, "Videos", Parent)

	first, _, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	second, _, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	Remove(first)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("Remove left %q behind (err=%v)", first, err)
	}
	if _, err := os.Stat(parent); err != nil {
		t.Fatalf("Remove deleted the shared parent while another run still had a directory in it: %v", err)
	}

	Remove(second)
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("the shared parent %q survived removal of the last directory in it (err=%v)", parent, err)
	}
}

// Make does the sweeping, so a long-running install does not accumulate a
// directory per killed run in the user's Videos folder forever.
func TestMakeSweepsStaleSiblings(t *testing.T) {
	home := fakeHome(t)
	parent := filepath.Join(home, "Videos", Parent)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("creating parent: %v", err)
	}
	abandoned := filepath.Join(parent, "probe-abandoned")
	if err := os.Mkdir(abandoned, 0o700); err != nil {
		t.Fatalf("creating abandoned dir: %v", err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(abandoned, stale, stale); err != nil {
		t.Fatalf("ageing abandoned dir: %v", err)
	}

	dir, _, err := Make("probe", 24*time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("a stale directory survived a later Make (err=%v); nothing under $HOME is reclaimed by the system, so these accumulate", err)
	}
}

// resolvedRel relativises path against home with every symlink resolved, which
// is what AppArmor does before applying snapd's home rule. A lexical check
// passes happily on a path that resolves onto another filesystem.
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

// A media directory symlinked to another drive is a common arrangement, and
// os.Stat and os.MkdirAll both follow symlinks without complaint. Such a base
// must be rejected: snapd's home rule is enforced on the resolved path, so a
// staging directory that lands on /mnt is denied to a confined player exactly
// as /tmp is — and reporting it as visible would be a lie.
func TestMakeRejectsABaseSymlinkedOutsideHome(t *testing.T) {
	home := fakeHome(t)
	outside := t.TempDir()
	videos := filepath.Join(home, "Videos")
	if err := os.Remove(videos); err != nil {
		t.Fatalf("clearing Videos: %v", err)
	}
	if err := os.Symlink(outside, videos); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if rel := resolvedRel(t, home, dir); strings.HasPrefix(rel, "..") {
		t.Errorf("Make returned %q, which resolves outside the home directory (rel %q); a symlinked media directory is followed by os.MkdirAll but denied by snapd", dir, rel)
	}
	if !visible {
		t.Errorf("Make fell back to the temp dir; the last-resort base under $HOME was still usable")
	}
	if _, err := os.Stat(filepath.Join(outside, Parent)); err == nil {
		t.Errorf("Make created %s on the far side of the symlink before rejecting it", Parent)
	}
}

// The shared parent can be the symlink even when the base is sound.
func TestMakeRejectsAParentSymlinkedOutsideHome(t *testing.T) {
	home := fakeHome(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "Videos", Parent)); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if rel := resolvedRel(t, home, dir); strings.HasPrefix(rel, "..") {
		t.Errorf("Make returned %q, which resolves outside the home directory (rel %q)", dir, rel)
	}
	if !visible {
		t.Errorf("Make fell back to the temp dir instead of the next usable base under $HOME")
	}
}

// Rejecting escapes must not reject a symlink that stays inside $HOME — those
// are reachable, and over-rejecting would push users to the broken temp-dir
// fallback for no reason.
func TestMakeAcceptsASymlinkThatStaysInsideHome(t *testing.T) {
	home := fakeHome(t)
	real := filepath.Join(home, "media")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("creating %s: %v", real, err)
	}
	videos := filepath.Join(home, "Videos")
	if err := os.Remove(videos); err != nil {
		t.Fatalf("clearing Videos: %v", err)
	}
	if err := os.Symlink(real, videos); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if !visible {
		t.Fatalf("Make rejected %q, but it resolves inside the home directory and a confined player can read it", dir)
	}
	if rel := resolvedRel(t, home, dir); !strings.HasPrefix(rel, "media"+string(filepath.Separator)) {
		t.Errorf("Make resolved to %q (rel %q); the symlinked base inside $HOME should have been used", dir, rel)
	}
}
