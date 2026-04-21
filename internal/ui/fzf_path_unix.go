//go:build !windows

package ui

import "os/exec"

// fzfBinary returns the path to the fzf binary.
func fzfBinary() (string, error) {
	return exec.LookPath("fzf")
}
