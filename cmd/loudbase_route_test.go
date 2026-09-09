package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// withStubbedYTS installs a stubbed YTS behind the route's seam without
// touching cfg — unlike withYTSRoute (typeroute_test.go), which installs a cfg
// of its own. These tests exist to prove that the *real* config path produces
// a cfg the route reads as auto, so handing them a hand-built cfg would defeat
// the whole point.
func withStubbedYTS(t *testing.T, stub *ytsCatalogStub) {
	t.Helper()
	prev := newYTSProvider
	newYTSProvider = func() provider.Provider { return stub }
	t.Cleanup(func() { newYTSProvider = prev })
}

// assertMovieRoutesToYTS drives the route with a movie selection and fails
// unless it came back on the stubbed YTS carrying YTS's own ID.
func assertMovieRoutesToYTS(t *testing.T, yts *ytsCatalogStub, how string) {
	t.Helper()
	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	got, routed := routeByType(primary, sel)
	if got != provider.Provider(yts) {
		t.Fatalf("with %s, routeByType returned %T (cfg.Base = %q), want the YTS provider: a loudly spelled auto is still auto, so movies take the YTS route", how, got, cfg.Base)
	}
	if routed.ID != "yts/1745" {
		t.Fatalf("with %s, routed selection ID = %q, want yts/1745", how, routed.ID)
	}
}

// writeConfig plants a config.toml the real loader will read, and points the
// loader at it on both unix and windows.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // config.configDir, unix
	t.Setenv("APPDATA", dir)         // config.configDir, windows
	sub := filepath.Join(dir, "lobster")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing config.toml: %v", err)
	}
}

// The three readers of Base compare it three different ways — mayStreamTorrent
// case-insensitively, baseIsAuto exactly, newProvider by substring — and the
// answer to that is one canonical spelling at the boundary, not three tolerant
// comparisons. config.NormalizeBase is that boundary, and Validate calls it for
// both inputs that reach it (the file at Load, --base at applyConfig's
// re-validation).
//
// TestALoudlySpelledAutoIsAutoForEveryReaderOfBase (autobase_test.go) asserts
// the readers agree once Validate has run. These three assert the thing that
// actually matters to a user: that the *real* entry points run it, so `base =
// "AUTO"` still ends with a movie on YTS. That is the guarantee the exact
// comparison in baseIsAuto depends on, so it is the one worth pinning — remove
// the NormalizeBase call from Validate and this fails on the routeByType
// assertion, not on a comparison in isolation.
func TestAConfigFileSpellingAutoLoudlyStillRoutesMoviesToYTS(t *testing.T) {
	// tbcpl_feed off: its catalogue fetch is a network call, and nothing here
	// needs it.
	writeConfig(t, "base = \"AUTO\"\ntbcpl_feed = false\n")
	withOutputFlags(t, false, "")
	restoreFlags := saveFlagGlobals(t)
	t.Cleanup(restoreFlags)
	flagBase = ""

	prev := cfg
	t.Cleanup(func() { cfg = prev })

	yts := matrixOnYTS()
	withStubbedYTS(t, yts)

	// The real PersistentPreRunE. The command is deliberately not marked as a
	// playback command, so loadConfig stops after applyConfig rather than
	// deciding the storage backend — the base is what is under test.
	if err := loadConfig(refCmd(t), nil); err != nil {
		t.Fatalf("loadConfig() = %v, want nil", err)
	}
	// The route first: it is the guarantee that matters, and asserting the
	// canonical spelling ahead of it would report a broken boundary as a
	// spelling mismatch rather than as a movie that missed YTS.
	assertMovieRoutesToYTS(t, yts, "base = \"AUTO\" in config.toml")
	if cfg.Base != config.BaseAuto {
		t.Fatalf("after loading a config file with base = \"AUTO\", cfg.Base = %q, want %q", cfg.Base, config.BaseAuto)
	}
}

// The flag is the second input, and it lands on cfg.Base *after* Load's
// Validate, so it relies on applyConfig re-validating.
func TestABaseFlagSpellingAutoLoudlyStillRoutesMoviesToYTS(t *testing.T) {
	writeConfig(t, "tbcpl_feed = false\n")
	withOutputFlags(t, false, "")
	restoreFlags := saveFlagGlobals(t)
	t.Cleanup(restoreFlags)
	flagBase = "  AUTO  "

	prev := cfg
	t.Cleanup(func() { cfg = prev })

	yts := matrixOnYTS()
	withStubbedYTS(t, yts)

	if err := loadConfig(refCmd(t), nil); err != nil {
		t.Fatalf("loadConfig() = %v, want nil", err)
	}
	assertMovieRoutesToYTS(t, yts, "--base \"  AUTO  \"")
	if cfg.Base != config.BaseAuto {
		t.Fatalf("after --base with a loud auto, cfg.Base = %q, want %q", cfg.Base, config.BaseAuto)
	}
}

// A ref's stamped base is the third input and the only one that never passes
// through Validate: decodeRef checks Type strictly and Base not at all. It
// normalises in applyRefBase instead, and this is what says so end to end — a
// ref minted under a loud auto must still route a movie to YTS.
func TestARefStampedWithALoudAutoStillRoutesMoviesToYTS(t *testing.T) {
	unsetStorageEnv(t)
	withOutputFlags(t, false, "")
	restoreFlags := saveFlagGlobals(t)
	t.Cleanup(restoreFlags)

	prev := cfg
	cfg = &config.Config{Base: "soap2day", Quality: "1080", Player: "mpv"}
	t.Cleanup(func() { cfg = prev })

	yts := matrixOnYTS()
	withStubbedYTS(t, yts)
	captureWarnings(t)

	applyRefBase(refCmd(t), playRef{Base: "AUTO"})

	assertMovieRoutesToYTS(t, yts, "a ref stamped base \"AUTO\"")
	if cfg.Base != config.BaseAuto {
		t.Fatalf("after a ref stamped base %q, cfg.Base = %q, want %q", "AUTO", cfg.Base, config.BaseAuto)
	}
}
