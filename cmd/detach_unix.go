//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

// detachSpawnAttr puts the supervisor in its own process group so it survives
// the agent's shell exiting.
func detachSpawnAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// processAlive reports whether the pid is still running. Signal 0 performs the
// permission and existence checks without delivering anything.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
