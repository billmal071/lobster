//go:build !windows

package player

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shortHome points os.UserHomeDir at a throwaway home whose path is short
// enough that a socket beneath it still fits in sun_path.
//
// t.TempDir() is not usable here: on macOS it sits under a long
// /var/folders/... path, and a socket path below that can exceed the 104-byte
// limit these tests are precisely about — the fallback would fire and the
// assertions would pass for the wrong reason.
func shortHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "lbh-")
	if err != nil {
		t.Skipf("no short temp directory to build a fake home in: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.Mkdir(filepath.Join(home, "Videos"), 0o755); err != nil {
		t.Fatalf("creating fake Videos dir: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

// The bug: an mpv packaged as a snap runs with a private /tmp, so the socket it
// creates for an --input-ipc-server path under os.TempDir() is not the file
// lobster then dials. The dial fails for the whole retry bound and the watch is
// recorded as untracked — silently, so resume simply never works.
func TestNewIPCSocketIsNotInTheSystemTempDir(t *testing.T) {
	home := shortHome(t)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	if !ipc.sandboxVisible {
		t.Errorf("newIPCSocket reported the socket invisible to a confined mpv, but a usable home was available")
	}
	if got := filepath.Dir(filepath.Dir(ipc.path)); got == os.TempDir() {
		t.Errorf("mpv socket %q was placed in the system temp dir; an mpv packaged as a snap has its own private /tmp and would create its socket somewhere lobster never dials", ipc.path)
	}
	if rel, err := filepath.Rel(home, ipc.path); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("mpv socket %q is not under the home directory %q, so a confined mpv cannot reach it", ipc.path, home)
	}
	// snapd denies any path whose first component below $HOME is dot-prefixed.
	rel, err := filepath.Rel(home, ipc.path)
	if err != nil {
		t.Fatalf("relativising %q against %q: %v", ipc.path, home, err)
	}
	if top := strings.Split(filepath.ToSlash(rel), "/")[0]; strings.HasPrefix(top, ".") {
		t.Errorf("top-level component %q of %q is hidden; snapd denies a confined mpv any such path", top, rel)
	}
}

// Placement is worth nothing if the path cannot carry a socket. bind and
// connect both fail outright on a path too long for sun_path, so exercise the
// real syscalls rather than asserting on the string length.
func TestIPCSocketPathCanActuallyCarryASocket(t *testing.T) {
	shortHome(t)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	ln, err := net.Listen("unix", ipc.path)
	if err != nil {
		t.Fatalf("binding a socket at %q (%d bytes): %v", ipc.path, len(ipc.path), err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	conn, err := ipc.dial()
	if err != nil {
		t.Fatalf("dialling %q: %v", ipc.path, err)
	}
	_ = conn.Close()
}

// A home deep enough to overflow sun_path must fall back rather than hand mpv a
// path neither process can bind. The fallback is the broken-for-snaps location,
// so it has to say so.
func TestNewIPCSocketFallsBackWhenTheHomePathIsTooLongForSunPath(t *testing.T) {
	home := shortHome(t)

	// Nest until a socket below this home cannot fit. The suffix Make adds is
	// "/Videos/.lobster/mpv-<10 random digits>/socket".
	deep := home
	for len(deep) < maxSocketPath {
		deep = filepath.Join(deep, "aaaaaaaaaaaaaaaaaaaa")
	}
	if err := os.MkdirAll(filepath.Join(deep, "Videos"), 0o755); err != nil {
		t.Fatalf("creating deep fake home: %v", err)
	}
	t.Setenv("HOME", deep)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	if ipc.sandboxVisible {
		t.Errorf("newIPCSocket claimed %q is sandbox-visible, but it had to leave the home directory to fit in sun_path", ipc.path)
	}
	if len(ipc.path) > maxSocketPath {
		t.Fatalf("fallback socket path %q is %d bytes, over the %d-byte sun_path limit — neither mpv nor lobster could bind it", ipc.path, len(ipc.path), maxSocketPath)
	}
	ln, err := net.Listen("unix", ipc.path)
	if err != nil {
		t.Fatalf("binding the fallback socket at %q: %v", ipc.path, err)
	}
	_ = ln.Close()
}

// A dial failure caused by confinement reads like a network problem unless the
// message says otherwise — and must not cry sandbox when the socket was
// somewhere mpv could have reached.
func TestIPCSandboxHintOnlyFiresWhenTheSocketIsOutOfReach(t *testing.T) {
	if hint := ipcSandboxHint(&ipcSocket{sandboxVisible: true}); hint != "" {
		t.Errorf("ipcSandboxHint blamed confinement for a socket under $HOME: %q", hint)
	}
	hint := ipcSandboxHint(&ipcSocket{sandboxVisible: false})
	if hint == "" {
		t.Fatal("ipcSandboxHint said nothing about a socket a confined mpv cannot see")
	}
	if !strings.Contains(hint, "snap") {
		t.Errorf("the hint does not name the cause a reader has to act on: %q", hint)
	}
}

// The socket directory lives under $HOME now, where nothing reclaims it.
func TestIPCCleanupRemovesTheSocketDirectory(t *testing.T) {
	shortHome(t)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	dir := filepath.Dir(ipc.path)

	ipc.cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup left the socket directory %q behind (err=%v); unlike /tmp, nothing under $HOME is reclaimed by the system", dir, err)
	}
}
