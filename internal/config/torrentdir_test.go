package config

import (
	"os"
	"path/filepath"
	"testing"
)

// An empty torrent_dir must stay empty: it is the sentinel that lets
// torrentstream pick the default location, and an absolutised "" would read as
// a user-chosen directory — the process's working directory, wherever that is.
func TestExpandTorrentDirKeepsTheUnsetSentinel(t *testing.T) {
	c := &Config{}
	dir, err := c.ExpandTorrentDir()
	if err != nil {
		t.Fatalf("ExpandTorrentDir: %v", err)
	}
	if dir != "" {
		t.Errorf("ExpandTorrentDir() = %q for an unset torrent_dir, want \"\" so the default applies", dir)
	}
}

// The tilde form is what a user writes in a TOML file, and nothing downstream
// expands it: an unexpanded "~" is created as a literal directory named "~"
// in the working directory.
func TestExpandTorrentDirExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	c := &Config{TorrentDir: "~/media/torrents"}
	dir, err := c.ExpandTorrentDir()
	if err != nil {
		t.Fatalf("ExpandTorrentDir: %v", err)
	}
	want := filepath.Join(home, "media", "torrents")
	if dir != want {
		t.Errorf("ExpandTorrentDir() = %q, want %q", dir, want)
	}
}

// A relative path must come back absolute: the data directory outlives any
// chdir the process makes, and a relative one would resolve differently later.
func TestExpandTorrentDirMakesPathsAbsolute(t *testing.T) {
	c := &Config{TorrentDir: "scratch"}
	dir, err := c.ExpandTorrentDir()
	if err != nil {
		t.Fatalf("ExpandTorrentDir: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("ExpandTorrentDir() = %q, want an absolute path", dir)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if want := filepath.Join(wd, "scratch"); dir != want {
		t.Errorf("ExpandTorrentDir() = %q, want %q", dir, want)
	}
}

// The default install must not pin a directory of its own: torrentstream's
// default goes through internal/userdir, which refuses a media directory
// symlinked off the home volume and sweeps abandoned siblings. A literal
// default here would bypass all of that.
func TestDefaultTorrentDirIsUnset(t *testing.T) {
	if got := Default().TorrentDir; got != "" {
		t.Errorf("Default().TorrentDir = %q, want \"\" so internal/userdir picks the location", got)
	}
}
