//go:build !windows

package cmd

import "syscall"

// detachSpawnAttr puts the supervisor in its own process group so it survives
// the agent's shell exiting.
func detachSpawnAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
