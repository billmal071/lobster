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

// unresolvableHome blanks every variable os.UserHomeDir consults so a "~/" path
// has nothing to expand against, and skips if the platform still answers.
func unresolvableHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("this platform resolves a home directory without the environment")
	}
}

// A failed home lookup does not leave a worse spelling of the right directory,
// it names a different one: filepath.Abs turns an unexpanded "~/torrents" into
// "<working dir>/~/torrents", so lobster creates a literal "~" wherever it was
// started and fills it with tens of gigabytes. Printing that path shows what
// happened but not why, and the next run with a different working directory
// writes somewhere else again.
func TestExpandTorrentDirReportsAHomeLookupFailure(t *testing.T) {
	unresolvableHome(t)

	c := &Config{TorrentDir: "~/torrents"}
	dir, err := c.ExpandTorrentDir()
	if err == nil {
		t.Fatalf("ExpandTorrentDir() = %q with a nil error while the home directory cannot be resolved; a literal \"~\" under the working directory is not the configured location", dir)
	}
	if dir != "" {
		t.Errorf("ExpandTorrentDir() returned %q alongside its error; a caller that logs the error but keeps the path still writes to the wrong place", dir)
	}
}

// download_dir and torrent_dir are two keys in one TOML file written by one
// user, so an unresolvable home has to mean the same thing for both. It did
// not: ExpandDownloadDir reported it and ExpandTorrentDir returned a path.
func TestDownloadAndTorrentDirAgreeOnAFailedHomeLookup(t *testing.T) {
	unresolvableHome(t)

	c := &Config{DownloadDir: "~/Videos/lobster", TorrentDir: "~/torrents"}
	_, downErr := c.ExpandDownloadDir()
	_, torrErr := c.ExpandTorrentDir()
	if (downErr == nil) != (torrErr == nil) {
		t.Errorf("ExpandDownloadDir err = %v but ExpandTorrentDir err = %v; the same tilde in the same file must fail the same way", downErr, torrErr)
	}
}

// expandTilde keeps the silent passthrough its own doc comment promises, now
// that it shares the lookup with expandTildeErr. Config.Sources uses it for
// playlist paths, where the original spelling is what belongs in the "cannot
// read" message; an empty string there would report a missing file named "".
func TestExpandTildeKeepsTheInputWhenHomeIsUnresolvable(t *testing.T) {
	unresolvableHome(t)

	const in = "~/playlists/mine.m3u"
	if got := expandTilde(in); got != in {
		t.Errorf("expandTilde(%q) = %q, want it unchanged so the caller reports the path the user wrote", in, got)
	}
}
