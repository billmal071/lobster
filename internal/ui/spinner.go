package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// StartSpinner starts a simple animated CLI loader attached to stderr.
// It returns a closer function that MUST be called to stop the spinner and restore the cursor.
func StartSpinner(msg string) func() {
	stopChan := make(chan struct{})
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

	go func() {
		fmt.Fprintf(os.Stderr, "\033[?25l") // Hide terminal cursor
		i := 0
		for {
			select {
			case <-stopChan:
				fmt.Fprintf(os.Stderr, "\r\033[K\033[?25h") // Clear current line and show cursor
				return
			default:
				fmt.Fprintf(os.Stderr, "\r\033[35m%s\033[0m %s", frames[i], msg)
				i = (i + 1) % len(frames)
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stopChan)
			// Small delay to ensure the goroutine receives the close signal and clears the prompt
			time.Sleep(10 * time.Millisecond)
		})
	}
}
