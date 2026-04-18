//go:build !windows

package player

// vlcBinaryName returns the VLC binary name for the current platform.
func vlcBinaryName() string {
	return "vlc"
}
