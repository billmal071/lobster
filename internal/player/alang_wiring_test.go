package player

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lobster/internal/media"
)

// fakeMPV writes a stand-in mpv onto PATH that records the arguments it was
// given. Windows cannot run a #!/bin/sh script, and LookPath there resolves
// .bat via PATHEXT, so the stub has to differ by platform — the assertion does
// not, which is the point.
func fakeMPV(t *testing.T) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "args.txt")

	name, script := "mpv", "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+logPath+"\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, script = "mpv.bat", "@echo off\r\necho %* > \""+logPath+"\"\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// The arg builder being correct is worth nothing if Play never calls it. This
// puts a fake mpv on PATH and asserts the flag actually reaches the process.
func TestMPVPlayPassesAudioLanguageToTheProcess(t *testing.T) {
	log := fakeMPV(t)

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
