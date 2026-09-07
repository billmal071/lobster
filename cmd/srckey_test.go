package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// useTempRefKey points the ref-key secret at a fresh temp dir and resets the
// memoized value, restoring both afterwards. The secret is loaded once per
// process, so a test that wants a *different* key than its neighbours has to
// clear that memo explicitly.
func useTempRefKey(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir) // unix (config.dataDir)
	t.Setenv("LOCALAPPDATA", dir)  // windows
	resetRefKey(t)
	return dir
}

// A fresh sync.Once is assigned rather than the previous one restored: go vet
// rejects copying a sync.Once, and there is nothing worth preserving anyway —
// clearing it on cleanup lets the next test re-derive the secret from
// whatever data dir is then in effect.
func resetRefKey(t *testing.T) {
	t.Helper()
	refKeyOnce, refKeyVal = sync.Once{}, nil
	t.Cleanup(func() { refKeyOnce, refKeyVal = sync.Once{}, nil })
}

// The whole point of keying the digest: an attacker holding a ref knows the
// scheme, host and path already (they are in the ref's display Source), so an
// unkeyed hash would let them confirm a guessed username and password
// offline. Under an HMAC the same source must digest differently on two
// installations, which is what makes that offline test impossible.
func TestSourceKeyIsKeyedNotAPlainHash(t *testing.T) {
	const raw = "https://host.example/get.php?username=alice&password=hunter2"

	useTempRefKey(t)
	first := sourceKey(raw)
	if first == "" {
		t.Fatal("sourceKey returned empty with a writable data dir")
	}

	// A second installation: new data dir, new secret, same source.
	useTempRefKey(t)
	second := sourceKey(raw)
	if second == "" {
		t.Fatal("sourceKey returned empty with a writable data dir")
	}
	if first == second {
		t.Fatal("sourceKey produced the same digest under two different secrets; it is not keyed")
	}
}

// Within one installation the digest must be stable, or every ref would stop
// matching its own channel the next time lobster ran.
func TestSourceKeyIsStableAcrossProcessesOnOneInstallation(t *testing.T) {
	const raw = "https://host.example/get.php?username=alice&password=hunter2"

	dir := useTempRefKey(t)
	first := sourceKey(raw)

	// Same data dir, fresh process: the secret is re-read from disk rather
	// than regenerated.
	resetRefKey(t)
	if got := sourceKey(raw); got != first {
		t.Fatalf("sourceKey = %q on a second run, want the first run's %q (data dir %s)", got, first, dir)
	}
}

// Two sources differing only in credentials must not share a digest — this is
// the collision that src_key exists to break.
func TestSourceKeySeparatesSameEndpointCredentials(t *testing.T) {
	useTempRefKey(t)
	alice := sourceKey("https://host.example/get.php?username=alice&password=a1")
	bob := sourceKey("https://host.example/get.php?username=bob&password=b2")
	if alice == bob {
		t.Fatal("two subscriptions on one endpoint produced the same source key")
	}
}

// With nowhere to persist a secret, sourceKey must return "" so the ref omits
// src_key and matching falls back to display Source. It must never fall back
// to an unkeyed digest, which would hand out the guessable value the keying
// exists to prevent.
func TestSourceKeyIsEmptyWhenNoSecretCanBePersisted(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	// A regular file where the data directory should be: MkdirAll fails, so
	// the key cannot be written.
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(blocked, "lobster"))
	t.Setenv("LOCALAPPDATA", filepath.Join(blocked, "lobster"))
	resetRefKey(t)

	if got := sourceKey("https://host.example/get.php?username=alice"); got != "" {
		t.Fatalf("sourceKey = %q with no writable data dir, want \"\"", got)
	}
}

// A truncated key file is never used as a key — half a key would weaken every
// digest minted for the life of the installation — but neither is it repaired
// in place. Repair would mean publishing by replacement, and two processes
// repairing the same file would each clobber the other, breaking refs the
// loser had already minted. It degrades to "" instead, which sends matching
// back to display Source until someone removes the file.
func TestRefKeyShortFileDegradesRatherThanBeingOverwritten(t *testing.T) {
	dir := useTempRefKey(t)
	path := filepath.Join(dir, "lobster", "refkey")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("writing short key: %v", err)
	}

	if got := sourceKey("https://host.example/get.php"); got != "" {
		t.Fatalf("sourceKey = %q over a truncated key file, want \"\" (the display-source fallback)", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading key back: %v", err)
	}
	if string(b) != "short" {
		t.Fatalf("the truncated file was rewritten (now %d bytes); it must be left alone", len(b))
	}

	// Removing it restores normal operation on the next call.
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing key: %v", err)
	}
	resetRefKey(t)
	if got := sourceKey("https://host.example/get.php"); got == "" {
		t.Fatal("sourceKey still empty after the corrupt key file was removed")
	}
}

// The truncated-file path must be as race-free as the fresh-install one:
// every racer either adopts one published key or reports none, and none of
// them rewrites the file out from under the others.
func TestRefKeyConcurrentAccessToTruncatedFileStaysConsistent(t *testing.T) {
	dir := useTempRefKey(t)
	path := filepath.Join(dir, "lobster", "refkey")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("writing short key: %v", err)
	}

	const racers = 8
	keys := make([][]byte, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i] = loadOrCreateRefKey()
		}(i)
	}
	wg.Wait()

	for i, k := range keys {
		if !bytes.Equal(k, keys[0]) {
			t.Fatalf("racer %d disagreed with racer 0 over a truncated key file", i)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading key back: %v", err)
	}
	if string(b) != "short" {
		t.Fatalf("a racer rewrote the truncated file (now %d bytes); none may", len(b))
	}
}

// TestRefKeyConcurrentCreationAgreesOnOneKey pins the publication race.
//
// The first implementation published with os.Rename, which *replaces* the
// destination. Two starts together each minted a key and the second rename
// overwrote the first — and the loser had already handed its own key to its
// caller, so every ref it minted was signed with a secret no longer on disk
// and stopped matching its channel on the next run. Publication is by
// os.Link now, which fails when the destination exists, so the loser adopts
// the winner's key instead of clobbering it.
//
// Goroutines rather than processes: loadOrCreateRefKey holds no in-process
// lock, so every one of them races on the filesystem exactly as separate
// processes would, and the failure mode is identical.
func TestRefKeyConcurrentCreationAgreesOnOneKey(t *testing.T) {
	useTempRefKey(t)

	const racers = 8
	keys := make([][]byte, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			keys[i] = loadOrCreateRefKey()
		}(i)
	}
	wg.Wait()

	for i, k := range keys {
		if len(k) != refKeyBytes {
			t.Fatalf("racer %d got a %d-byte key, want %d", i, len(k), refKeyBytes)
		}
		if !bytes.Equal(k, keys[0]) {
			t.Fatalf("racer %d disagreed with racer 0; a ref minted by one would not match under the other", i)
		}
	}

	// And the key every racer returned is the one actually on disk, so a
	// later run re-reads the same secret.
	resetRefKey(t)
	if got := refSourceSecret(); !bytes.Equal(got, keys[0]) {
		t.Fatal("the persisted key differs from the one the racers returned")
	}
}
