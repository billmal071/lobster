//go:build !windows

package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"lobster/internal/media"
)

const fakeMPVProgressThenZero = `#!/usr/bin/env python3
import socket, sys
path = [a.split("=", 1)[1] for a in sys.argv if a.startswith("--input-ipc-server=")][0]
s = socket.socket(socket.AF_UNIX)
s.bind(path)
s.listen(1)
c, _ = s.accept()
f = c.makefile("rwb")
f.readline()
f.readline()
for pos in (30.0, 0.0):
    f.write(('{"event":"property-change","id":1,"name":"time-pos","data":%s}\n' % pos).encode())
    f.flush()
c.close()
`

func TestMPVPlayKeepsMeasuredZeroAfterProgressWithinStartupWindow(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mpv"), []byte(fakeMPVProgressThenZero), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := New("mpv", "").Play(&media.Stream{URL: "http://127.0.0.1:1/x.m3u8"}, "t", 0, nil)
	if err != nil {
		t.Fatalf("playback that reported progress was treated as a failed start: %v", err)
	}
	if res.PositionUnknown || res.Position != 0 {
		t.Errorf("got position %v unknown=%v, want a measured 0", res.Position, res.PositionUnknown)
	}
}

const fakeMPVZeroThenCrash = `#!/usr/bin/env python3
import socket, sys
path = [a.split("=", 1)[1] for a in sys.argv if a.startswith("--input-ipc-server=")][0]
s = socket.socket(socket.AF_UNIX)
s.bind(path)
s.listen(1)
c, _ = s.accept()
f = c.makefile("rwb")
f.readline()
f.readline()
f.write(b'{"event":"property-change","id":1,"name":"time-pos","data":0.0}\n')
f.flush()
c.close()
sys.exit(2)
`

func TestMPVPlayErrorBeforeAnyProgressReportsNoMeasurement(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mpv"), []byte(fakeMPVZeroThenCrash), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := New("mpv", "").Play(&media.Stream{URL: "http://127.0.0.1:1/x.m3u8"}, "t", 600, nil)
	if err == nil {
		t.Fatal("an mpv that exited with status 2 was not reported as an error")
	}
	if !res.PositionUnknown {
		t.Errorf("error exit that never got past 0 returned %+v, want PositionUnknown so the 0 cannot replace a resume point", res)
	}
}

func TestMPVPlayFailedStartReportsNoMeasurement(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	res, err := New("mpv", "").Play(&media.Stream{URL: "http://127.0.0.1:1/x.m3u8"}, "t", 0, nil)
	if err == nil {
		t.Fatal("an mpv that exits at once with no playback was not reported as a failed start")
	}
	if !res.PositionUnknown {
		t.Errorf("failed start returned %+v, want PositionUnknown so a retry keeps its resume offset", res)
	}
}
