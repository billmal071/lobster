package subtitle

import (
	"os"
	"testing"
)

// TestMain points os.UserHomeDir at a throwaway directory for every test in
// this package.
//
// Subtitle staging lives under $HOME, so a test that calls NewTempDir without
// setting up its own fake home would create a lobster directory in the
// developer's (and CI's) real home. Tests that assert on the home layout still
// set HOME themselves; this is the floor.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "lobster-subtitle-home-*")
	if err != nil {
		os.Exit(1)
	}
	os.Setenv("HOME", home)        // unix
	os.Setenv("USERPROFILE", home) // windows
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
