//go:build !windows

package player

// mpvBinaryName returns the mpv binary name for the current platform.
func mpvBinaryName() string {
	return "mpv"
}
