package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	agentLiveSources = func(context.Context) []string { return []string{path} }
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
// it would have played, restoring the original with t.Cleanup. It also stubs
// agentPlayerCheck to report a player as always available: agentPlayerCheck
// runs before source loading and ref resolution (cmd/liveref.go), so without
// this stub every test using stubLivePlayer would silently depend on whether
// mpv happens to be on the host's PATH — passing on a dev machine that has
// it, and failing everywhere else (including CI, which never installs a
// player). Restored with t.Cleanup like the player seam itself.
func stubLivePlayer(t *testing.T, played *string) {
	t.Helper()
	old := agentPlayLive
	agentPlayLive = func(stream *media.Stream, title string) error {
		*played = stream.URL
		return nil
	}
	t.Cleanup(func() { agentPlayLive = old })

	oldCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = oldCheck })
}

// useSources points agentLiveSources/agentLiveTV at exactly these paths (in
// order), restoring both with t.Cleanup. Used where a test needs more than
// one source — a single-source load either fully succeeds or fully fails
// (LoadContext errors when every source fails), so it can never exercise the
// "this channel's own playlist is down, but others loaded" branch.
func useSources(t *testing.T, paths ...string) {
	t.Helper()
	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func(context.Context) []string { return paths }
	agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })
}

// TestPlayLiveDetachWithNoSourcesIsNotConfigured covers the ordering bug
// where --detach forked a supervisor child *before* the "any live sources
// configured" check. With no sources, the child immediately exits 1 with
// not_configured, the parent's liveness wait sees it die, and the parent
// reports exit 3 (providers_failed) naming a log file instead — contradicting
// the documented contract that not_configured at exit 1 means "configure a
// source", not "retry". The fix moves the sources check above the --detach
// fork so a misconfigured invocation never forks at all.
//
// agentPlayerCheck is stubbed because it runs before either check and this
// test has nothing to do with player availability; without the stub this
// test would depend on whether mpv happens to be on the host's PATH.
func TestPlayLiveDetachWithNoSourcesIsNotConfigured(t *testing.T) {
	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	oldSources := agentLiveSources
	agentLiveSources = func(context.Context) []string { return nil }
	t.Cleanup(func() { agentLiveSources = oldSources })

	// If this ever reaches agentLiveTV, the sources check did not run first
	// (it would have returned before any provider is needed either way, but
	// this also catches a --detach fork: playDetached never calls
	// agentLiveTV directly, so a call here would mean control reached past
	// the point this test means to guard).
	oldTV := agentLiveTV
	agentLiveTV = func(sources []string) *provider.LiveTV {
		t.Error("agentLiveTV was called: the not-configured check did not fire first")
		return provider.NewLiveTV(sources)
	}
	t.Cleanup(func() { agentLiveTV = oldTV })

	ref := liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u")
	err := runAgentCmdErr(t, playCmd, "--ref", ref, "--detach")
	assertExit(t, err, exitUsage)
	assertErrCode(t, "not_configured")
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

// TestPlayLiveRejectsExplicitEmptyDownload covers "--download ”": a value
// check (flagDownload != "") lets this through silently indistinguishable
// from --download never having been passed at all, so live playback would
// proceed uncaught. Passing --download at all is the usage error for a live
// ref, regardless of the value given — the same class of bug as the
// season/episode zero-value cases above.
func TestPlayLiveRejectsExplicitEmptyDownload(t *testing.T) {
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

	err := runAgentCmdErr(t, playCmd, "--ref", liveRefFor(t, "bbc1.uk", "BBC One", "src.m3u"), "--download", "")
	assertExit(t, err, exitUsage)
	if called {
		t.Error("provider was constructed before the download rejection")
	}
	if played != "" {
		t.Error("the player seam was invoked for an explicit --download ''")
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
	var played string
	stubLivePlayer(t, &played)

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
// TestFilterBySourceMatchesSanitizedHTTPSource exercises filterBySource with
// a real http(s) credential-bearing source, the one configuration where the
// ref's stored source and the channel's raw one actually diverge. Every
// live-TV test fixture elsewhere in this file uses a local temp-file path,
// for which displaySource is the identity — so those fixtures cannot tell a
// correct comparison from one that regressed to comparing raw strings. This
// test can.
func TestFilterBySourceMatchesSanitizedHTTPSource(t *testing.T) {
	raw := "https://user:pass@host.example/get.php?username=alice&password=hunter2&type=m3u_plus"
	if displaySource(raw) == raw {
		t.Fatalf("fixture is broken: displaySource did not change %q", raw)
	}

	chs := []provider.Channel{{Name: "News HD", Source: raw}}
	r := playRef{Source: displaySource(raw), SrcKey: sourceKey(raw)}

	got := filterBySource(chs, r)
	if len(got) != 1 {
		t.Fatalf("filterBySource(chs, r) = %d channels, want 1 (raw source %q)", len(got), raw)
	}
}

// TestFilterBySourceSeparatesSameEndpointCredentials is the reason a ref
// carries src_key at all. Two Xtream subscriptions to one server differ only
// in the query string, which displaySource strips wholesale — so both reduce
// to the same display source. A ref minted from the first must not match a
// channel loaded from the second: if the channel has since vanished from the
// first playlist, matching on the shared display source would leave exactly
// one match and resolveLiveRef would play another subscriber's stream as a
// confident, unambiguous hit.
func TestFilterBySourceSeparatesSameEndpointCredentials(t *testing.T) {
	alice := "https://host.example/get.php?username=alice&password=a1&type=m3u_plus"
	bob := "https://host.example/get.php?username=bob&password=b2&type=m3u_plus"
	if displaySource(alice) != displaySource(bob) {
		t.Fatalf("fixture is broken: %q and %q must share a display source", alice, bob)
	}

	chs := []provider.Channel{{Name: "News HD", TVGID: "news.hd", Source: bob}}
	r := playRef{Title: "News HD", TVGID: "news.hd", Source: displaySource(alice), SrcKey: sourceKey(alice)}

	if got := filterBySource(chs, r); len(got) != 0 {
		t.Fatalf("filterBySource kept %d channel(s) from another subscription on the same endpoint, want 0", len(got))
	}
}

// A ref minted before src_key existed carries only the sanitized Source.
// It must still narrow by that, rather than silently matching every playlist.
func TestFilterBySourceHonoursPreSrcKeyRefs(t *testing.T) {
	raw := "https://host.example/get.php?username=alice&password=a1"
	other := "https://elsewhere.example/playlist.m3u"

	chs := []provider.Channel{
		{Name: "News HD", Source: raw},
		{Name: "News HD", Source: other},
	}
	got := filterBySource(chs, playRef{Source: displaySource(raw)})
	if len(got) != 1 || got[0].Source != raw {
		t.Fatalf("filterBySource(chs, legacy ref) = %+v, want the one channel from %q", got, raw)
	}
}

// TestRefSourceInFailedSourcesMatchesSanitizedHTTPSource is
// refSourceInFailedSources' counterpart to the filterBySource test above: it
// exercises the FailedSources() comparison in resolveLiveRef with a real
// http(s) credential-bearing source, which is the only configuration where
// the ref's stored source and the raw one diverge.
func TestRefSourceInFailedSourcesMatchesSanitizedHTTPSource(t *testing.T) {
	raw := "https://user:pass@host.example/get.php?username=alice&password=hunter2&type=m3u_plus"
	if displaySource(raw) == raw {
		t.Fatalf("fixture is broken: displaySource did not change %q", raw)
	}

	r := playRef{Source: displaySource(raw), SrcKey: sourceKey(raw)}
	if !refSourceInFailedSources([]string{raw}, r) {
		t.Fatalf("refSourceInFailedSources([%q], r) = false, want true", raw)
	}

	// The other subscription on the same endpoint being down says nothing
	// about this ref's playlist: reporting it as down would send the caller
	// to retry a playlist that never failed.
	bob := "https://host.example/get.php?username=bob&password=b2&type=m3u_plus"
	if refSourceInFailedSources([]string{bob}, r) {
		t.Fatalf("refSourceInFailedSources([%q], r) = true for a different subscription on the same endpoint", bob)
	}

	// A pre-src_key ref still matches on the sanitized source alone.
	if !refSourceInFailedSources([]string{raw}, playRef{Source: displaySource(raw)}) {
		t.Fatal("a ref minted before src_key existed must still match its failed source")
	}
}

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

// TestPlayLiveRefRefusesChannelFromAnotherSubscriptionOnTheSameEndpoint is
// the end-to-end replay of the collision src_key exists to close, over real
// (loopback) http sources rather than the temp-file paths every other fixture
// here uses — a file path has no query string, so it cannot express two
// sources that differ only in their credentials.
//
// Both sources are the same Xtream endpoint with different subscriber
// credentials, so they share a display source. The ref is minted from
// alice's playlist; by the time it is replayed the channel is gone from
// alice's playlist and only bob's copy remains. Matching on the display
// source would leave exactly one candidate and play bob's stream as a
// confident hit. It must instead report the channel as gone.
func TestPlayLiveRefRefusesChannelFromAnotherSubscriptionOnTheSameEndpoint(t *testing.T) {
	const withChannel = "#EXTM3U\n#EXTINF:-1 tvg-id=\"news.hd\",News HD\nhttp://example.invalid/news.m3u8\n"
	const withoutChannel = "#EXTM3U\n#EXTINF:-1 tvg-id=\"other.hd\",Other HD\nhttp://example.invalid/other.m3u8\n"

	// aliceHasChannel flips after the ref is minted: the channel leaves
	// alice's playlist, which is what makes bob's copy the only candidate.
	aliceHasChannel := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("username") == "alice" && !aliceHasChannel {
			_, _ = io.WriteString(w, withoutChannel)
			return
		}
		_, _ = io.WriteString(w, withChannel)
	}))
	t.Cleanup(srv.Close)

	alice := srv.URL + "/get.php?username=alice&password=a1&type=m3u_plus"
	bob := srv.URL + "/get.php?username=bob&password=b2&type=m3u_plus"
	if displaySource(alice) != displaySource(bob) {
		t.Fatalf("fixture is broken: %q and %q must share a display source", alice, bob)
	}

	p := provider.NewLiveTV([]string{alice})
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("loading alice's playlist: %v", err)
	}
	chs := p.AllChannels()
	if len(chs) != 1 {
		t.Fatalf("fixture is broken: alice's playlist has %d channels, want 1", len(chs))
	}
	ref, err := liveChannelRef(chs[0])
	if err != nil {
		t.Fatalf("encoding ref: %v", err)
	}

	aliceHasChannel = false
	useSources(t, alice, bob)
	var played string
	stubLivePlayer(t, &played)

	err = runAgentCmdErr(t, playCmd, "--ref", ref)
	assertExit(t, err, exitNoResults)
	if played != "" {
		t.Fatalf("played %q from another subscription on the same endpoint; the channel is gone from the ref's own playlist", played)
	}
}
