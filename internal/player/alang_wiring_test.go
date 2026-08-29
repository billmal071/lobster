package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lobster/internal/media"
)

// The arg builder being correct is worth nothing if Play never calls it. This
// puts a fake mpv on PATH and asserts the flag actually reaches the process.
func TestMPVPlayPassesAudioLanguageToTheProcess(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args.txt")
	fake := filepath.Join(dir, "mpv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := New("mpv", "english")
	// mpv exits immediately; Play may report an error from the IPC hop, which is
	// irrelevant here — the assertion is on what got executed.
	_, _ = p.Play(&media.Stream{URL: "http://127.0.0.1:1/x.m3u8"}, "t", 0, nil)

	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("fake mpv was never executed: %v", err)
	}
	if !strings.Contains(string(got), "--alang=eng,en,english") {
		t.Errorf("--alang not passed to mpv; args were:\n%s", got)
	}
}
