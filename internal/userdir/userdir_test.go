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
//
// The home it hands back is reached through a symlink, mirroring macOS, where
// $TMPDIR is /var/folders/... and resolves to /private/var/folders/... . Make
// returns paths under the *resolved* home, so an assertion that compares them
// against the unresolved one passes on a plain Linux home and fails on a Mac.
// Building the difference in here means every platform exercises it.
func fakeHome(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "Videos"), 0o755); err != nil {
		t.Fatalf("creating fake Videos dir: %v", err)
	}
	home := filepath.Join(base, "home")
	if err := os.Symlink(real, home); err != nil {
		// Windows without developer mode, say: fall back to the plain
		// directory rather than skipping the whole suite.
		home = real
	}
	t.Setenv("HOME", home)        // unix
	t.Setenv("USERPROFILE", home) // windows
	return home
}

// underHome reports whether path, resolved, is inside home, resolved. Comparing
// unresolved paths is the bug this helper exists to avoid.
func underHome(t *testing.T, home, path string) bool {
	t.Helper()
	return !strings.HasPrefix(resolvedRel(t, home, path), "..")
}

// inSystemTempDir reports whether path sits directly in the system temp dir.
// os.TempDir carries a trailing separator on macOS and none on Linux, so the
// comparison has to be cleaned as well as resolved.
func inSystemTempDir(t *testing.T, path string) bool {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(filepath.Clean(os.TempDir()))
	if err != nil {
		t.Fatalf("resolving the system temp dir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %q: %v", path, err)
	}
	return filepath.Dir(resolved) == tmp
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
	if inSystemTempDir(t, dir) {
		t.Errorf("Make created %q directly in the system temp dir; a confined helper has its own private /tmp and cannot reach it", dir)
	}
	if !underHome(t, home, dir) {
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

	rel := resolvedRel(t, home, dir)
	top := strings.Split(filepath.ToSlash(rel), "/")[0]
	if strings.HasPrefix(top, ".") {
		t.Errorf("top-level component %q of %q is hidden; snapd denies a confined helper any path below $HOME whose first component is dot-prefixed", top, rel)
	}
}

// A caller that cannot use any home-based directory still gets somewhere to
// work, and is told the handoff is not sandbox-safe rather than left to guess.
func TestMakeFallsBackWhenEveryHomeCandidateIsVetoed(t *testing.T) {
	home := fakeHome(t)

	// Veto anything under $HOME, leaving only the temp-dir fallback. The
	// comparison resolves both sides: a lexical one calls a resolved-home
	// candidate "outside" and accepts it, which is the opposite of the intent.
	dir, visible, err := Make("probe", time.Hour, func(candidate string) bool {
		return !underHome(t, home, candidate)
	})
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if visible {
		t.Errorf("Make claimed %q is visible to a confined helper, but every home candidate was vetoed", dir)
	}
	if !inSystemTempDir(t, dir) {
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
	if !inSystemTempDir(t, dir) {
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
		// All three are lobster's, so what the sweep does here turns on the
		// prefix and the age, which is what this test is about.
		if err := MarkOwned(d); err != nil {
			t.Fatalf("marking %s: %v", d, err)
		}
	}
	for _, d := range []string{mine, theirs} {
		// Staleness is read off the marker, which is what a live run refreshes;
		// the directory is aged alongside it only so nothing here looks fresh
		// for the wrong reason.
		for _, p := range []string{filepath.Join(d, Marker), d} {
			if err := os.Chtimes(p, stale, stale); err != nil {
				t.Fatalf("ageing %s: %v", p, err)
			}
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

// The pathname check that proposes a base can be stale the moment it returns:
// another process can swap a validated component for an outward symlink before
// the directory is created. Creation is therefore root-relative, which turns
// that race into a failed candidate rather than an escape.
//
// The window is a few syscalls wide in production, far too narrow to hit by
// running the two concurrently and hoping — a version built on os.MkdirAll
// survives that test comfortably, which would make it worthless. So the test
// widens the window deterministically: the swap happens inside the window, via
// the hook Make calls between resolving a base and creating anything in it.
func TestMakeNeverCreatesOutsideHomeWhenTheBaseIsSwappedAfterValidation(t *testing.T) {
	home := fakeHome(t)
	outside := t.TempDir()
	videos := filepath.Join(home, "Videos")

	prev := afterBaseResolved
	t.Cleanup(func() { afterBaseResolved = prev })
	swapped := false
	afterBaseResolved = func(base string) {
		if base != "Videos" || swapped {
			return
		}
		// Videos has just been validated as a real directory inside $HOME.
		// Replace it with a link out before anything is created in it.
		if err := os.RemoveAll(videos); err != nil {
			t.Errorf("removing %q: %v", videos, err)
			return
		}
		if err := os.Symlink(outside, videos); err != nil {
			t.Skipf("symlinks unavailable on this platform: %v", err)
		}
		swapped = true
	}

	dir, visible, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if !swapped {
		t.Fatal("the hook never fired, so the window was never opened and this test proves nothing")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatalf("reading %q: %v", outside, err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("Make created %v outside the home directory by following a symlink swapped in after the base was validated", names)
	}
	// Having refused the swapped base, it must still have produced something
	// usable from a later one rather than giving up.
	if rel := resolvedRel(t, home, dir); strings.HasPrefix(rel, "..") {
		t.Errorf("Make returned %q, which resolves outside the home directory (rel %q)", dir, rel)
	}
	if !visible {
		t.Errorf("Make fell back to the temp dir; another base under $HOME was still usable")
	}
}

// The sweep's parent is a directory the user chose, so a stale directory that
// happens to match the prefix may be theirs. Deleting it is recursive and
// unrecoverable, so the marker — not the name — is what authorises removal.
func TestPruneStaleSparesUnmarkedDirectories(t *testing.T) {
	parent := t.TempDir()
	theirs := filepath.Join(parent, "probe-backups")
	theirFile := filepath.Join(theirs, "keepme.txt")
	if err := os.Mkdir(theirs, 0o700); err != nil {
		t.Fatalf("creating %s: %v", theirs, err)
	}
	if err := os.WriteFile(theirFile, []byte("not lobster's"), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", theirFile, err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(theirs, stale, stale); err != nil {
		t.Fatalf("ageing %s: %v", theirs, err)
	}

	PruneStale(parent, "probe", 24*time.Hour)

	if _, err := os.Stat(theirFile); err != nil {
		t.Errorf("the sweep deleted %q, which lobster never created and did not mark: %v", theirFile, err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("the sweep deleted the unmarked directory %q: %v", theirs, err)
	}
}

// Make is where the marker comes from for every caller that does not create its
// own directory, so a directory it made must be sweepable by a later run.
func TestMakeMarksTheDirectoryItCreates(t *testing.T) {
	fakeHome(t)

	dir, _, err := Make("probe", time.Hour, nil)
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	t.Cleanup(func() { Remove(dir) })

	if _, err := os.Stat(filepath.Join(dir, Marker)); err != nil {
		t.Errorf("Make left %q unmarked, so no later run may sweep it: %v", dir, err)
	}
}

// A directory's modification time moves when entries are created or removed, not
// when their contents change — so a torrent client writing gigabytes into files
// it allocated at the start of the run leaves the directory's mtime frozen at
// the moment the run began. A session outlasting staleAfter would then look
// abandoned to a second lobster starting up, which would delete the payload out
// from under the player. The marker's timestamp, refreshed while the run is
// live, is the liveness record; the directory's own is not evidence of anything.
func TestPruneStaleSparesARunWhoseMarkerIsFresh(t *testing.T) {
	parent := t.TempDir()
	live := filepath.Join(parent, "probe-live")
	payload := filepath.Join(live, "movie.mkv")
	if err := os.Mkdir(live, 0o700); err != nil {
		t.Fatalf("creating %s: %v", live, err)
	}
	if err := os.WriteFile(payload, make([]byte, 64), 0o600); err != nil {
		t.Fatalf("allocating %s: %v", payload, err)
	}
	if err := MarkOwned(live); err != nil {
		t.Fatalf("marking %s: %v", live, err)
	}
	// The run started two days ago and allocated its files then; nothing has
	// been added to or removed from the directory since.
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(live, stale, stale); err != nil {
		t.Fatalf("ageing %s: %v", live, err)
	}
	// It is still running, and says so the only way it can.
	now := time.Now()
	if err := os.Chtimes(filepath.Join(live, Marker), now, now); err != nil {
		t.Fatalf("refreshing the marker in %s: %v", live, err)
	}

	PruneStale(parent, "probe", 24*time.Hour)

	if _, err := os.Stat(payload); err != nil {
		t.Errorf("the sweep deleted %q out from under a run that is still refreshing its marker: %v", payload, err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the sweep deleted the live run's directory %q: %v", live, err)
	}
}

// The other half of the same rule: once the marker stops being refreshed the
// directory must still be swept, however recently its contents were touched.
// Nothing under $HOME is reclaimed by the system, so a run killed without
// cleaning up would otherwise leave tens of gigabytes behind forever.
func TestPruneStaleSweepsARunWhoseMarkerWentStale(t *testing.T) {
	parent := t.TempDir()
	dead := filepath.Join(parent, "probe-dead")
	payload := filepath.Join(dead, "movie.mkv")
	if err := os.Mkdir(dead, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dead, err)
	}
	if err := os.WriteFile(payload, make([]byte, 64), 0o600); err != nil {
		t.Fatalf("allocating %s: %v", payload, err)
	}
	if err := MarkOwned(dead); err != nil {
		t.Fatalf("marking %s: %v", dead, err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{dead, filepath.Join(dead, Marker)} {
		if err := os.Chtimes(p, stale, stale); err != nil {
			t.Fatalf("ageing %s: %v", p, err)
		}
	}
	// The payload was written seconds before the process was killed, so the
	// file's own timestamp is fresh. That must not save the directory.

	PruneStale(parent, "probe", 24*time.Hour)

	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("a run that stopped refreshing its marker survived the sweep (stat err %v); payloads would accumulate unbounded", err)
	}
}

// Touch is the only way a run can say it is still alive, so it has to work on a
// marker that has gone stale and on one that is no longer there at all.
func TestTouchRefreshesTheMarkerAndWritesItBackIfGone(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, Marker)
	if err := MarkOwned(dir); err != nil {
		t.Fatalf("marking %s: %v", dir, err)
	}
	stale := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(marker, stale, stale); err != nil {
		t.Fatalf("ageing the marker: %v", err)
	}

	if err := Touch(dir); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	fi, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat marker: %v", err)
	}
	if !fi.ModTime().After(stale) {
		t.Errorf("Touch left the marker at %v; a run that cannot say it is alive gets its payload swept", fi.ModTime())
	}

	if err := os.Remove(marker); err != nil {
		t.Fatalf("removing the marker: %v", err)
	}
	if err := Touch(dir); err != nil {
		t.Fatalf("Touch on a missing marker: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Touch did not write the marker back, so the directory is now neither sweepable nor protected: %v", err)
	}
}

// Keepalive must refresh straight away — a run whose first refresh is an interval
// away is unprotected for that interval — and must keep refreshing until it is
// stopped, which is what carries a session past staleAfter.
func TestKeepaliveRefreshesTheMarkerUntilStopped(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, Marker)
	if err := MarkOwned(dir); err != nil {
		t.Fatalf("marking %s: %v", dir, err)
	}
	age := func() {
		stale := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(marker, stale, stale); err != nil {
			t.Fatalf("ageing the marker: %v", err)
		}
	}
	mtime := func() time.Time {
		fi, err := os.Stat(marker)
		if err != nil {
			t.Fatalf("stat marker: %v", err)
		}
		return fi.ModTime()
	}
	cutoff := time.Now().Add(-24 * time.Hour)

	age()
	// The interval is injected so this test drives the loop instead of waiting
	// out a real one.
	stop := Keepalive(dir, 5*time.Millisecond)
	t.Cleanup(stop)
	if !mtime().After(cutoff) {
		t.Fatalf("Keepalive did not refresh the marker before returning (mtime %v); the run is unprotected until the first tick", mtime())
	}

	// Now prove the loop ticks rather than only refreshing once.
	age()
	deadline := time.Now().Add(2 * time.Second)
	for !mtime().After(cutoff) {
		if time.Now().After(deadline) {
			t.Fatalf("the marker was still at %v after 2s; Keepalive refreshed once and stopped, so a run outlasting staleAfter is still swept", mtime())
		}
		time.Sleep(time.Millisecond)
	}

	stop()
	stop() // idempotent: Close may run twice.
	// A refresh already in flight when stop returned lands inside the first
	// window, so the second window is what shows the loop is really over.
	time.Sleep(50 * time.Millisecond)
	settled := mtime()
	time.Sleep(50 * time.Millisecond)
	if got := mtime(); !got.Equal(settled) {
		t.Errorf("the marker moved from %v to %v after stop; the goroutine outlived the run and would keep a deleted directory looking alive", settled, got)
	}
}
