//go:build !windows

package player

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// shortCheckpointInterval pins the checkpoint throttle for a test and
// restores the production value afterward.
func shortCheckpointInterval(t *testing.T, d time.Duration) {
	t.Helper()
	prev := checkpointInterval
	checkpointInterval = d
	t.Cleanup(func() { checkpointInterval = prev })
}

// serveMPVIPCEvents is like serveMPVIPC but emits an arbitrary event script,
// so tests can feed the tracker zero positions or long sequences.
func serveMPVIPCEvents(path string, events []string) <-chan error {
	done := make(chan error, 1)
	go func() {
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

		r := bufio.NewReader(conn)
		for i := 0; i < 2; i++ {
			if _, err := r.ReadString('\n'); err != nil {
				done <- fmt.Errorf("fake mpv: reading observe_property %d: %w", i, err)
				return
			}
		}

		for _, e := range events {
			if _, err := conn.Write([]byte(e + "\n")); err != nil {
				done <- fmt.Errorf("fake mpv: writing event: %w", err)
				return
			}
		}
		done <- nil
	}()
	return done
}

func timePosEvent(pos float64) string {
	return fmt.Sprintf(`{"event":"property-change","id":1,"name":"time-pos","data":%g}`, pos)
}

func durationEvent(dur float64) string {
	return fmt.Sprintf(`{"event":"property-change","id":2,"name":"duration","data":%g}`, dur)
}

// checkpointRecorder collects checkpoint invocations thread-safely.
type checkpointRecorder struct {
	mu    sync.Mutex
	calls [][2]float64
}

func (r *checkpointRecorder) record(pos, dur float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]float64{pos, dur})
}

func (r *checkpointRecorder) snapshot() [][2]float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]float64(nil), r.calls...)
}

// While playback runs, the tracker must hand positions to the checkpoint
// callback (throttled by checkpointInterval) — a hard shutdown that kills mpv
// and the tracker at once must cost at most one interval of resume position,
// not the whole watch.
func TestTrackPlaybackInvokesCheckpointDuringPlayback(t *testing.T) {
	shortCheckpointInterval(t, 0)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	serverDone := serveMPVIPCEvents(ipc.path, []string{
		durationEvent(5400),
		timePosEvent(100),
		timePosEvent(200),
	})

	rec := &checkpointRecorder{}
	m := &MPV{}
	m.SetCheckpoint(rec.record)
	pos, dur, _ := m.trackPlayback(ipc, make(chan struct{}))
	if pos != 200 || dur != 5400 {
		t.Fatalf("trackPlayback = %g, %g; want 200, 5400 (final state must be unaffected by checkpointing)", pos, dur)
	}

	calls := rec.snapshot()
	if len(calls) == 0 {
		t.Fatal("checkpoint callback never invoked during playback; a hard shutdown would lose the whole watch position")
	}
	for _, c := range calls {
		if c[0] <= 0 {
			t.Fatalf("checkpoint invoked with position %g; zero positions must never be checkpointed", c[0])
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// The throttle is real: with the interval far longer than the playback, no
// checkpoint fires. Otherwise every IPC event would trigger a history write.
func TestTrackPlaybackCheckpointHonoursInterval(t *testing.T) {
	shortCheckpointInterval(t, time.Hour)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	serverDone := serveMPVIPCEvents(ipc.path, []string{
		durationEvent(5400),
		timePosEvent(100),
		timePosEvent(200),
		timePosEvent(300),
	})

	rec := &checkpointRecorder{}
	m := &MPV{}
	m.SetCheckpoint(rec.record)
	if pos, _, _ := m.trackPlayback(ipc, make(chan struct{})); pos != 300 {
		t.Fatalf("trackPlayback position = %g, want 300", pos)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("checkpoint fired %d times inside a %s interval; want 0 (throttle must hold)", len(calls), time.Hour)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// A zero or negative time-pos (mpv emits these at load and on some seeks)
// must never reach the callback — writing 0 would clobber a real resume
// position from an earlier watch, mirroring the exit-time skip-when-zero rule.
func TestTrackPlaybackNeverCheckpointsZeroPosition(t *testing.T) {
	shortCheckpointInterval(t, 0)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	serverDone := serveMPVIPCEvents(ipc.path, []string{
		timePosEvent(0),
		durationEvent(5400),
		timePosEvent(0),
		timePosEvent(150),
	})

	rec := &checkpointRecorder{}
	m := &MPV{}
	m.SetCheckpoint(rec.record)
	if pos, _, _ := m.trackPlayback(ipc, make(chan struct{})); pos != 150 {
		t.Fatalf("trackPlayback position = %g, want 150", pos)
	}

	calls := rec.snapshot()
	if len(calls) == 0 {
		t.Fatal("no checkpoint at all; the positive position must still be checkpointed")
	}
	for _, c := range calls {
		if c[0] <= 0 {
			t.Fatalf("checkpoint invoked with position %g; must skip zero positions", c[0])
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

// The callback does file I/O on its own goroutine so a slow write cannot back
// up the IPC event loop — but every invocation must complete before
// trackPlayback returns, because the caller runs the (more precise) exit-time
// save immediately after and a late checkpoint would clobber it.
func TestTrackPlaybackCheckpointNeverOutlivesTracking(t *testing.T) {
	shortCheckpointInterval(t, 0)

	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	serverDone := serveMPVIPCEvents(ipc.path, []string{
		durationEvent(5400),
		timePosEvent(100),
		timePosEvent(200),
		timePosEvent(300),
	})

	rec := &checkpointRecorder{}
	slow := func(pos, dur float64) {
		time.Sleep(150 * time.Millisecond) // a slow history write
		rec.record(pos, dur)
	}
	m := &MPV{}
	m.SetCheckpoint(slow)
	if pos, _, _ := m.trackPlayback(ipc, make(chan struct{})); pos != 300 {
		t.Fatalf("trackPlayback position = %g, want 300", pos)
	}

	before := len(rec.snapshot())
	if before == 0 {
		t.Fatal("checkpoint callback never completed before trackPlayback returned")
	}
	time.Sleep(300 * time.Millisecond)
	if after := len(rec.snapshot()); after != before {
		t.Fatalf("checkpoint callback ran after trackPlayback returned (%d calls grew to %d); it would interleave with the exit-time save", before, after)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
