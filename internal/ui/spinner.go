package ui

import (
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// spinnerVisible reports whether an animated spinner belongs on stderr.
//
// A spinner is a cursor-addressing animation: every frame is "\r" plus an SGR
// sequence, which only means anything to a terminal. Where stderr is a file or
// a pipe the carriage returns do not overwrite, so a 10-second resolve at 80ms
// a frame deposits ~125 frames — several KB of "⠋ Fetching..." — into whatever
// is on the other end.
//
// That is not merely untidy for `play --detach`: cmd/detach.go points the
// child's stderr at a log file, and that log is the only diagnostic an agent
// has once the parent has already returned {"status":"started"}. It has to
// stay readable.
//
// Overridable so tests can drive both branches; a pty is not worth requiring.
var spinnerVisible = func() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// StartSpinner starts a simple animated CLI loader attached to stderr.
// It returns a closer function that MUST be called to stop the spinner and
// restore the cursor. When stderr is not a terminal the spinner is suppressed
// entirely and the closer is a no-op — see spinnerVisible.
//
// The closer waits for the animation goroutine to finish, so once it returns
// nothing further is written to stderr. It used to close a channel and sleep
// 10ms instead, which does not hold: the goroutine slept 80ms between frames,
// so its cursor-restore landed up to 80ms after the caller had moved on — over
// whatever the next writer had already printed.
func StartSpinner(msg string) func() {
	if !spinnerVisible() {
		return func() {}
	}

	stopChan := make(chan struct{})
	done := make(chan struct{})
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	go func() {
		defer close(done)
		fmt.Fprintf(os.Stderr, "\033[?25l") // Hide terminal cursor
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i = (i + 1) % len(frames) {
			fmt.Fprintf(os.Stderr, "\r\033[35m%s\033[0m %s", frames[i], msg)
			// The stop check sits on the same select as the frame delay, so a
			// stop is observed immediately rather than after the current
			// frame's sleep has run to completion.
			select {
			case <-stopChan:
				fmt.Fprintf(os.Stderr, "\r\033[K\033[?25h") // Clear current line and show cursor
				return
			case <-ticker.C:
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() { close(stopChan) })
		// Outside the Once so a second call also waits rather than racing
		// ahead of a stop still in flight.
		<-done
	}
}
