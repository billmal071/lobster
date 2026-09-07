//go:build unix

package provider

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestLoadContextAbortsABlockedLocalSource pins the two guards that keep a
// local source from stranding the load, using the one file type that can
// block a read forever: a FIFO with no writer, whose open() blocks until
// someone opens it for writing, and nothing here ever does.
//
// The first guard is the stat check, which refuses a non-regular file before
// os.Open is reached. That is what this test exercises directly. It matters
// beyond the deadline: ctx frees the caller, but nothing interrupts a read
// already blocked in the kernel, so without the stat check a `play --ref`
// that resolves through another source would hold the abandoned goroutine
// for the length of the broadcast.
//
// The second is the ctx race in readFileContext, which covers what stat
// cannot see — a regular file on a stalled mount, blocking in the read after
// an unremarkable stat. TestReadFileContextHonoursCancelledContext covers
// that branch.
//
// If both regressed to a plain os.ReadFile the load would never return and
// this test would hang rather than fail, hence the explicit elapsed-time
// bound below, which trips first.
func TestLoadContextAbortsABlockedLocalSource(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "blocked.m3u")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported here: %v", err)
	}
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	p := NewLiveTV([]string{good, fifo})

	const budget = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- p.LoadContext(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("LoadContext with one good source must succeed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LoadContext never returned: a blocked local read is not bounded by ctx")
	}
	// Generous enough not to flake on a slow CI box, tight enough that a
	// read which merely finishes eventually would not pass for a cancelled one.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("LoadContext took %v, want it bounded near the %v deadline", elapsed, budget)
	}
	failed := p.FailedSources()
	if len(failed) != 1 || failed[0] != fifo {
		t.Fatalf("FailedSources() = %v, want [%s]", failed, fifo)
	}
	if len(p.AllChannels()) != 1 {
		t.Fatalf("got %d channels, want the one from the good source", len(p.AllChannels()))
	}
}

// TestReadFileContextHonoursCancelledContext covers the ctx branch that the
// FIFO test above no longer reaches now that non-regular files are refused
// before they are opened. A regular file with an already-cancelled context
// is the deterministic way to express "the read must not proceed": the file
// itself would read instantly, so a pass can only mean ctx was consulted.
func TestReadFileContextHonoursCancelledContext(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.m3u")
	writeFile(t, good, "#EXTM3U\n#EXTINF:-1,Alpha\nhttp://example.invalid/1.m3u8\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := readFileContext(ctx, good); !errors.Is(err, context.Canceled) {
		t.Fatalf("readFileContext with a cancelled context = %v, want context.Canceled", err)
	}
}

// A directory is not a playlist. It is refused by the same stat check as a
// FIFO, rather than reaching os.Open and failing later with a read error.
func TestReadFileContextRefusesADirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := readFileContext(context.Background(), dir); err == nil {
		t.Fatal("readFileContext on a directory must fail")
	}
}
