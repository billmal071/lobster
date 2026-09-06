package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// liveTestPath is the shared playlist file path for the current test's
// refFromPlaylist/usePlaylist pair, so a ref minted against one version of a
// playlist can be re-matched against a later version at the same path — the
// "playlist reordered" and "playlist deleted" scenarios both depend on the
// source string staying identical across the two calls.
var liveTestPath string

// liveTestFile returns the shared temp path for this test, creating it (and
// registering its own cleanup) on first use.
func liveTestFile(t *testing.T) string {
	t.Helper()
	if liveTestPath == "" {
		liveTestPath = filepath.Join(t.TempDir(), "live.m3u")
		t.Cleanup(func() { liveTestPath = "" })
	}
	return liveTestPath
}

// usePlaylist writes body to the shared playlist path and points
// agentLiveSources/agentLiveTV at it, restoring both with t.Cleanup.
func usePlaylist(t *testing.T, body string) {
	t.Helper()
	path := liveTestFile(t)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing playlist: %v", err)
	}
	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func() []string { return []string{path} }
	agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })
}

// refFromPlaylist loads body from the shared playlist path, finds the
// channel named name, and returns its ref.
func refFromPlaylist(t *testing.T, body, name string) string {
	t.Helper()
	path := liveTestFile(t)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing playlist: %v", err)
	}
	p := provider.NewLiveTV([]string{path})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("loading playlist: %v", err)
	}
	var found *provider.Channel
	for _, ch := range p.AllChannels() {
		if ch.Name == name {
			c := ch
			found = &c
			break
		}
	}
	if found == nil {
		t.Fatalf("no channel named %q in fixture", name)
	}
	ref, err := liveChannelRef(*found)
	if err != nil {
		t.Fatalf("encoding ref: %v", err)
	}
	return ref
}

// liveRefFor mints a ref directly, without going through a playlist.
func liveRefFor(t *testing.T, tvgID, title, source string) string {
	t.Helper()
	ref, err := encodeRef(playRef{
		ID:     "x",
		Title:  title,
		Type:   liveRefType,
		TVGID:  tvgID,
		Source: source,
	})
	if err != nil {
		t.Fatalf("encoding ref: %v", err)
	}
	return ref
}

// stubLivePlayer replaces agentPlayLive with one that records the stream URL
// it would have played, restoring the original with t.Cleanup.
func stubLivePlayer(t *testing.T, played *string) {
	t.Helper()
	old := agentPlayLive
	agentPlayLive = func(stream *media.Stream, title string) error {
		*played = stream.URL
		return nil
	}
	t.Cleanup(func() { agentPlayLive = old })
}

// useSources points agentLiveSources/agentLiveTV at exactly these paths (in
// order), restoring both with t.Cleanup. Used where a test needs more than
// one source — a single-source load either fully succeeds or fully fails
// (LoadContext errors when every source fails), so it can never exercise the
// "this channel's own playlist is down, but others loaded" branch.
func useSources(t *testing.T, paths ...string) {
	t.Helper()
	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func() []string { return paths }
	agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })
}

func TestPlayLiveRejectsSeasonAndEpisode(t *testing.T) {
	// Must fire before the provider is built: assert the seam is never called.
	called := false
	old := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV {
		called = true
		t.Errorf("agentLiveTV was called: the usage guard did not fire before the provider was built")
		return provider.NewLiveTV(nil)
	}
	t.Cleanup(func() { agentLiveTV = old })

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--season", "1")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the season/episode rejection")
	}
}

// TestPlayLiveRejectsExplicitZeroSeason covers "--season 0": a value check
// (flagSeason > 0) lets this through silently, discarding a flag the caller
// explicitly supplied instead of rejecting it. Passing --season or --episode
// at all is the usage error for a live ref, regardless of the value given.
func TestPlayLiveRejectsExplicitZeroSeason(t *testing.T) {
	called := false
	old := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV {
		called = true
		t.Errorf("agentLiveTV was called: the usage guard did not fire before the provider was built")
		return provider.NewLiveTV(nil)
	}
	t.Cleanup(func() { agentLiveTV = old })

	var played string
	stubLivePlayer(t, &played)

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--season", "0")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the season/episode rejection")
	}
	if played != "" {
		t.Error("the player seam was invoked for an explicit --season 0")
	}
}

// TestPlayLiveRejectsExplicitZeroEpisode is --episode's counterpart.
func TestPlayLiveRejectsExplicitZeroEpisode(t *testing.T) {
	called := false
	old := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV {
		called = true
		t.Errorf("agentLiveTV was called: the usage guard did not fire before the provider was built")
		return provider.NewLiveTV(nil)
	}
	t.Cleanup(func() { agentLiveTV = old })

	var played string
	stubLivePlayer(t, &played)

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--episode", "0")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the season/episode rejection")
	}
	if played != "" {
		t.Error("the player seam was invoked for an explicit --episode 0")
	}
}

func TestPlayLiveResolvesByTVGIDAfterPlaylistReorder(t *testing.T) {
	// The ID-drift case. The ref is minted against playlist A; playlist B has
	// the same two channels in the opposite order, so the ref's ID now names
	// a DIFFERENT channel. Feeding an empty playlist here would only prove
	// the empty case and let the real bug ship under a passing test.
	orderA := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Alpha\nhttp://example.invalid/alpha.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Beta\nhttp://example.invalid/beta.m3u8\n"
	orderB := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Beta\nhttp://example.invalid/beta.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"dup\",Alpha\nhttp://example.invalid/alpha.m3u8\n"

	var played string
	stubLivePlayer(t, &played)
	ref := refFromPlaylist(t, orderA, "Alpha")
	usePlaylist(t, orderB)

	// runAgentCmdErr is for the error path (it fails the test on success); a
	// resolved live ref is expected to play successfully, so use runAgentCmd.
	_ = runAgentCmd(t, playCmd, "--ref", ref)
	if played != "http://example.invalid/alpha.m3u8" {
		t.Fatalf("played %q, want Alpha's URL", played)
	}
}

func TestPlayLiveAmbiguousMatchRefuses(t *testing.T) {
	body := "#EXTM3U\n" +
		"#EXTINF:-1,Sports HD\nhttp://example.invalid/1.m3u8\n" +
		"#EXTINF:-1,Sports HD\nhttp://example.invalid/2.m3u8\n"
	var played string
	stubLivePlayer(t, &played)
	ref := refFromPlaylist(t, body, "Sports HD")
	usePlaylist(t, body)

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	assertErrCode(t, "ambiguous_channel")
	if played != "" {
		t.Fatal("an ambiguous ref must not play anything")
	}
}

func TestPlayLiveAbsentChannelIsNoResultsAndNeverResolves(t *testing.T) {
	// The second assertion is the one that matters: it proves a live ref
	// cannot leak into the title-search path.
	resolved := false
	oldResolve := agentResolveAndPlay
	agentResolveAndPlay = func(p provider.Provider, sel media.SearchResult, s, e int) error {
		resolved = true
		return nil
	}
	t.Cleanup(func() { agentResolveAndPlay = oldResolve })

	ref := refFromPlaylist(t, "#EXTM3U\n#EXTINF:-1 tvg-id=\"gone.uk\",Gone\nhttp://example.invalid/x.m3u8\n", "Gone")
	usePlaylist(t, "#EXTM3U\n#EXTINF:-1 tvg-id=\"other.uk\",Other\nhttp://example.invalid/y.m3u8\n")

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	if resolved {
		t.Fatal("a live ref reached resolveAndPlay — the title-search path")
	}
}

// TestPlayLiveDistinguishesDownPlaylistFromMissingChannel mints a ref against
// one playlist file among two configured sources, then deletes only that
// file before playing: the channel is not gone, its own playlist is down,
// and that needs a different exit code than "no longer in your playlists".
//
// This needs at least two sources. With only one, LoadContext itself returns
// an error the moment its only source fails (internal/provider/livetv.go:
// "if !anyOK && len(p.sources) > 0"), which playLiveRef already turns into
// exitProvidersFailed before resolveLiveRef ever runs — so a single-source
// version of this test would pass without ever reaching the FailedSources
// check resolveLiveRef makes, for a reason unrelated to what it claims to
// test.
func TestPlayLiveDistinguishesDownPlaylistFromMissingChannel(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.m3u")
	downPath := filepath.Join(dir, "down.m3u")

	if err := os.WriteFile(goodPath, []byte("#EXTM3U\n#EXTINF:-1 tvg-id=\"other.uk\",Other\nhttp://example.invalid/other.m3u8\n"), 0o600); err != nil {
		t.Fatalf("writing good fixture: %v", err)
	}
	downBody := "#EXTM3U\n#EXTINF:-1 tvg-id=\"x.uk\",X\nhttp://example.invalid/x.m3u8\n"
	if err := os.WriteFile(downPath, []byte(downBody), 0o600); err != nil {
		t.Fatalf("writing down fixture: %v", err)
	}

	p := provider.NewLiveTV([]string{downPath})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("loading down fixture: %v", err)
	}
	var ch provider.Channel
	for _, c := range p.AllChannels() {
		if c.Name == "X" {
			ch = c
		}
	}
	if ch.Name == "" {
		t.Fatal("channel X not found in down fixture")
	}
	ref, err := liveChannelRef(ch)
	if err != nil {
		t.Fatalf("encoding ref: %v", err)
	}

	// Now take downPath offline while goodPath keeps loading: with two
	// sources, LoadContext succeeds overall (one source is enough), and X's
	// absence must be explained by its own playlist having failed.
	if err := os.Remove(downPath); err != nil {
		t.Fatalf("removing fixture: %v", err)
	}
	useSources(t, goodPath, downPath)

	var played string
	stubLivePlayer(t, &played)

	err = runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitProvidersFailed)
	if played != "" {
		t.Fatal("must not play when the playlist is down")
	}
}

// TestPlayLiveAmbiguousMatchRefusesEvenAfterTitleNarrowing covers the branch
// unique to resolveLiveRef's tvg-id-collision narrowing: two channels that
// share BOTH tvg-id and exact folded title. Narrowing by title cannot break
// this tie, so it must still fail closed rather than silently picking one —
// the one guarantee the narrowing step itself could quietly break.
func TestPlayLiveAmbiguousMatchRefusesEvenAfterTitleNarrowing(t *testing.T) {
	body := "#EXTM3U\n" +
		"#EXTINF:-1 tvg-id=\"dup2\",Twin\nhttp://example.invalid/1.m3u8\n" +
		"#EXTINF:-1 tvg-id=\"dup2\",Twin\nhttp://example.invalid/2.m3u8\n"
	var played string
	stubLivePlayer(t, &played)
	ref := refFromPlaylist(t, body, "Twin")
	usePlaylist(t, body)

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	assertErrCode(t, "ambiguous_channel")
	if played != "" {
		t.Fatal("a tvg-id AND title collision must not play anything")
	}
}
