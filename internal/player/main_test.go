package player

import (
	"os"
	"testing"
)

// TestMain points os.UserHomeDir at a throwaway directory for every test in
// this package.
//
// newIPCSocket now stages mpv's IPC socket under $HOME rather than in the
// system temp dir, so any test that builds one — directly or through Play —
// would otherwise create a lobster directory in the developer's (and CI's)
// real home and leave the last-resort base behind. Individual tests that care
// about the home layout still set HOME themselves; this is the floor.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "lobster-player-home-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", home)        // unix
	os.Setenv("USERPROFILE", home) // windows
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
