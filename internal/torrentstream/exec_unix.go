//go:build !windows

package torrentstream

import (
	"os"
	"syscall"
)

// canExec reports that this platform can replace its own process image.
const canExec = true

// execSelf replaces this process with a fresh copy of the same binary, adding
// env to the environment. It does not return on success.
//
// execve rather than a child process: the pid, open file descriptors and
// controlling terminal all survive, so signal handling, the TUI's terminal and
// the exit status need no forwarding. Those are the costs of supervising a
// child, and this avoids them by not having one.
func execSelf(env string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The new image finds the variable set, so its own check is a no-op and
	// there is no exec loop.
	return syscall.Exec(exe, os.Args, append(os.Environ(), env))
}
