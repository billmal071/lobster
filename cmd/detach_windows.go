//go:build windows

package cmd

import "syscall"

// detachedProcess is Win32's DETACHED_PROCESS creation flag. Go's syscall
// package exports CREATE_NEW_PROCESS_GROUP but not this one, so it is spelled
// out here rather than left as a bare literal at the call site.
const detachedProcess = 0x00000008

// detachSpawnAttr detaches the supervisor from the console so it survives the
// agent's shell exiting. Setpgid does not exist on Windows.
func detachSpawnAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}
