package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/subtitle"
)

// Subtitles are no longer staged in /tmp, which the system reclaims on its own;
// they live under $HOME now, so whoever asks for them must be handed a cleanup
// and must call it — otherwise every episode of a session leaves a directory
// behind in the user's home.
func TestResolveSubtitlesCleanupRemovesStagingDir(t *testing.T) {
	prevCfg, prevNoSubs := cfg, flagNoSubs
	t.Cleanup(func() { cfg, flagNoSubs = prevCfg, prevNoSubs })
	cfg = &config.Config{SubsLanguage: "english"}
	flagNoSubs = false

	// Keep staging out of the real home directory.
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "Videos"), 0o755); err != nil {
		t.Fatalf("creating fake Videos dir: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// No network: the stub writes the file the real downloader would have
	// fetched, into the staging directory the caller created.
	prevDownload := subtitleDownload
	t.Cleanup(func() { subtitleDownload = prevDownload })
	var stagingDir string
	subtitleDownload = func(td *subtitle.TempDir, sub media.Subtitle, season, episode int) (string, error) {
		stagingDir = td.Path()
		path := filepath.Join(stagingDir, "stub.srt")
		if err := os.WriteFile(path, []byte("1\n"), 0o644); err != nil {
			return "", err
		}
		return path, nil
	}

	stream := &media.Stream{
		URL:       "https://example.invalid/stream.m3u8",
		Subtitles: []media.Subtitle{{URL: "https://example.invalid/a.srt", Label: "English", Language: "english"}},
	}

	subFiles, cleanup := resolveSubtitles(stream, "Some Film", 0, 0)
	if cleanup == nil {
		t.Fatal("resolveSubtitles returned a nil cleanup; the staging dir would leak")
	}
	if len(subFiles) != 1 {
		t.Fatalf("subFiles = %v, want one staged subtitle", subFiles)
	}
	if _, err := os.Stat(subFiles[0]); err != nil {
		t.Fatalf("staged subtitle %q missing: %v", subFiles[0], err)
	}

	cleanup()

	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %q survived cleanup (stat err %v)", stagingDir, err)
	}
}

// With subtitles switched off there is nothing to clean, but the caller still
// calls what it is handed.
func TestResolveSubtitlesCleanupNeverNilWhenDisabled(t *testing.T) {
	prevNoSubs := flagNoSubs
	t.Cleanup(func() { flagNoSubs = prevNoSubs })
	flagNoSubs = true

	subFiles, cleanup := resolveSubtitles(&media.Stream{}, "Some Film", 0, 0)
	if subFiles != nil {
		t.Errorf("subFiles = %v, want nil with --no-subs", subFiles)
	}
	if cleanup == nil {
		t.Fatal("resolveSubtitles returned a nil cleanup with --no-subs; callers would panic")
	}
	cleanup()
}
