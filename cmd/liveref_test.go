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

func TestPlayLiveRejectsSeasonAndEpisode(t *testing.T) {
	// Must fire before the provider is built: assert the seam is never called.
	called := false
	old := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV { called = true; return nil }
	t.Cleanup(func() { agentLiveTV = old })

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--season", "1")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the season/episode rejection")
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
// a real playlist file, then deletes that file before playing: the channel is
// not gone, its playlist is down, and those need different exit codes.
func TestPlayLiveDistinguishesDownPlaylistFromMissingChannel(t *testing.T) {
	body := "#EXTM3U\n#EXTINF:-1 tvg-id=\"x.uk\",X\nhttp://example.invalid/x.m3u8\n"
	ref := refFromPlaylist(t, body, "X")
	path := liveTestFile(t)

	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func() []string { return []string{path} }
	agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing fixture: %v", err)
	}

	var played string
	stubLivePlayer(t, &played)

	err := runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitProvidersFailed)
	if played != "" {
		t.Fatal("must not play when the playlist is down")
	}
}
