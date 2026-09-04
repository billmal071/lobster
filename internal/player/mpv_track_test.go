//go:build !windows

package player

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

// shortIPCDial shrinks the tracker's dial retry bound so "never connects"
// tests terminate promptly, restoring the production values afterward.
func shortIPCDial(t *testing.T, timeout time.Duration) {
	t.Helper()
	prevTimeout, prevInterval := ipcDialTimeout, ipcDialInterval
	ipcDialTimeout = timeout
	ipcDialInterval = 50 * time.Millisecond
	t.Cleanup(func() {
		ipcDialTimeout, ipcDialInterval = prevTimeout, prevInterval
	})
}

// serveMPVIPC pretends to be mpv's JSON IPC endpoint: it listens on path
// after startDelay, accepts one connection, reads the two observe_property
// commands, emits property-change events, and closes. The returned channel
// carries the server's outcome (nil on success); it never touches t from the
// goroutine, so an early test failure cannot panic on "Fail after test
// completed".
func serveMPVIPC(path string, startDelay time.Duration, pos, dur float64) <-chan error {
	done := make(chan error, 1)
	go func() {
		time.Sleep(startDelay)
		ln, err := net.Listen("unix", path)
		if err != nil {
			done <- fmt.Errorf("fake mpv: listen: %w", err)
			return
		}
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			done <- fmt.Errorf("fake mpv: accept: %w", err)
			return
		}
		defer conn.Close()

		// The tracker sends one observe_property line per property before it
		// starts reading events.
		r := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			if _, err := r.ReadString('\n'); err != nil {
				done <- fmt.Errorf("fake mpv: reading observe_property %d: %w", i, err)
				return
			}
		}

		events := []string{
			fmt.Sprintf(`{"event":"property-change","id":1,"name":"time-pos","data":%g}`+"\n", pos/2),
			fmt.Sprintf(`{"event":"property-change","id":2,"name":"duration","data":%g}`+"\n", dur),
			fmt.Sprintf(`{"event":"property-change","id":1,"name":"time-pos","data":%g}`+"\n", pos),
		}
		for _, e := range events {
			if _, err := conn.Write([]byte(e)); err != nil {
				done <- fmt.Errorf("fake mpv: writing event: %w", err)
				return
			}
		}
		done <- nil
	}()
	return done
}

// A slow network stream can hold mpv past socket creation for well over the
// old fixed 500ms grace, after which a single failed dial silently recorded
// the whole watch as position 0. The tracker must keep retrying: if mpv
// creates the socket at any point within the dial bound, the positions it
// reports are captured.
func TestTrackPlaybackConnectsWhenSocketAppearsLate(t *testing.T) {
	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	// 1.2s is comfortably beyond the old single-dial window at 500ms, and
	// keeps the whole test around 1.5s.
	serverDone := serveMPVIPC(ipc.path, 1200*time.Millisecond, 987.5, 5400)

	m := &MPV{}
	pos, dur := m.trackPlayback(ipc, make(chan struct{}))
	// Assert on the outcome first: if the tracker never connected, the server
	// is still blocked in Accept and draining serverDone would deadlock.
	if pos != 987.5 {
		t.Fatalf("trackPlayback position = %g, want 987.5 (a socket created after 1.2s must still be tracked)", pos)
	}
	if dur != 5400 {
		t.Fatalf("trackPlayback duration = %g, want 5400", dur)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// If mpv never creates the socket, the tracker must give up at the dial
// bound rather than spin forever.
func TestTrackPlaybackGivesUpAtDialBound(t *testing.T) {
	shortIPCDial(t, 300*time.Millisecond)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	start := time.Now()
	m := &MPV{}
	pos, dur := m.trackPlayback(ipc, make(chan struct{}))
	if pos != 0 || dur != 0 {
		t.Fatalf("trackPlayback = %g, %g; want 0, 0 when nothing ever listens", pos, dur)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("trackPlayback took %s to give up; the bound is %s", elapsed, 300*time.Millisecond)
	}
}

// When mpv exits before the socket ever appears (dead-on-arrival stream),
// Play closes the stop channel after cmd.Wait returns. The tracker must
// abandon its retry loop promptly then — otherwise a failed load would block
// Play for the full dial bound instead of the old ~500ms.
func TestTrackPlaybackStopsRetryingOnceProcessExits(t *testing.T) {
	shortIPCDial(t, 30*time.Second) // generous bound; stop must cut it short

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	stop := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(stop)
	}()

	start := time.Now()
	m := &MPV{}
	pos, dur := m.trackPlayback(ipc, stop)
	if pos != 0 || dur != 0 {
		t.Fatalf("trackPlayback = %g, %g; want 0, 0", pos, dur)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("trackPlayback took %s after stop closed; must return promptly, not run out the %s bound", elapsed, 30*time.Second)
	}
}
