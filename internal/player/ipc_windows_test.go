//go:build windows

package player

import (
	"testing"
	"time"
)

// dial must be a single fast attempt: retry pacing and the stop channel (mpv
// already exited) live in dialWithRetry. The old in-dial loop slept through
// 30 attempts (~6s) and could not observe stop, so a dead-on-arrival mpv
// blocked Play for the full loop after cmd.Wait had already returned.
func TestDialFailsFastWhenPipeAbsent(t *testing.T) {
	ipc, err := newIPCSocket()
	if err != nil {
		t.Fatalf("newIPCSocket: %v", err)
	}
	t.Cleanup(ipc.cleanup)

	start := time.Now()
	conn, err := ipc.dial()
	if err == nil {
		conn.Close()
		t.Fatal("dial succeeded with no pipe present")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("dial took %s with no pipe present; it must fail fast so dialWithRetry can honor stop between attempts", elapsed)
	}
}
