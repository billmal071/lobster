package ui

import (
	"os"
	"strings"
	"testing"
	"time"
)

// withPipedStderr points os.Stderr at a pipe for the duration of a test and
// returns everything written to it. A pipe is what stderr actually is for a
// detached run (cmd/detach.go hands the child a log file), so this is the real
// non-terminal shape, not a mock of one.
func withPipedStderr(t *testing.T, run func()) (out string) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	// Deferred rather than written after run(), with a named return so the
	// value survives: a t.Fatal inside run() calls runtime.Goexit, which runs
	// defers but never reaches a trailing statement — an unclosed write end
	// would leave the reader goroutine blocked on Read for the rest of the
	// run.
	defer func() { r.Close() }()
	defer func() { out = <-done }()
	defer func() { w.Close() }()

	run()
	return
}

// The production rule itself, with no stub in the way: the real
// spinnerVisible must say no when stderr is a pipe. Everything below relies on
// this, so it is tested first rather than assumed.
func TestSpinnerVisibleRejectsAPipedStderr(t *testing.T) {
	var got bool
	withPipedStderr(t, func() { got = spinnerVisible() })
	if got {
		t.Fatal("spinnerVisible() = true for a piped stderr, want false")
	}
}

// The regression: a detached play wrote several KB of spinner frames into the
// log the agent is told to read when the user reports nothing happened.
func TestStartSpinnerSilentWhenStderrIsNotATerminal(t *testing.T) {
	// No stub: withPipedStderr makes stderr genuinely non-terminal, which is
	// exactly the state cmd/detach.go puts the child in.
	out := withPipedStderr(t, func() {
		stop := StartSpinner("Fetching The Matrix media stream...")
		// Several frame intervals: 80ms each, so this would be ~5 frames.
		time.Sleep(400 * time.Millisecond)
		stop()
	})

	if out != "" {
		t.Fatalf("spinner wrote %d bytes to a non-terminal stderr: %q", len(out), out)
	}
}

// The suppression must not cost an interactive user their spinner.
func TestStartSpinnerAnimatesWhenStderrIsATerminal(t *testing.T) {
	prev := spinnerVisible
	spinnerVisible = func() bool { return true }
	t.Cleanup(func() { spinnerVisible = prev })

	out := withPipedStderr(t, func() {
		stop := StartSpinner("Searching...")
		time.Sleep(200 * time.Millisecond)
		stop()
	})

	if !strings.Contains(out, "Searching...") {
		t.Fatalf("spinner did not write its message to a terminal stderr: %q", out)
	}
	if !strings.Contains(out, "\033[?25l") {
		t.Fatalf("spinner did not hide the cursor: %q", out)
	}
	if !strings.Contains(out, "\033[35m") {
		t.Fatalf("spinner did not emit a coloured frame: %q", out)
	}
	// Asserted, not hand-waved: the closer joins the animation goroutine, so
	// the line clear and the cursor restore have provably landed by the time
	// stop() returns. A closer that merely signalled and slept would make this
	// a coin flip.
	if !strings.HasSuffix(out, "\r\033[K\033[?25h") {
		t.Fatalf("spinner did not clear the line and restore the cursor before stop() returned: %q", out)
	}
}

// The closer is returned in both branches and must be safe to call, and safe
// to call more than once.
func TestStartSpinnerCloserIsIdempotent(t *testing.T) {
	for _, visible := range []bool{true, false} {
		prev := spinnerVisible
		spinnerVisible = func() bool { return visible }

		withPipedStderr(t, func() {
			stop := StartSpinner("x")
			stop()
			stop()
		})

		spinnerVisible = prev
	}
}
