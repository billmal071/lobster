package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
	"lobster/internal/tui"
)

// recordingStreamProvider is a fallback-chain stand-in: it answers a title
// search with one exact match and records the episode ID Watch is asked for,
// which is the whole point — the arithmetic ID built by
// tryStreamProviderFallback (internal/resolver/probe.go) is what keeps a
// provider that cannot enumerate episodes playable.
type recordingStreamProvider struct {
	result media.SearchResult
	url    string

	mu           sync.Mutex
	lastEpisode  string
	watchCalled  bool
	watchCallCnt int
}

func (p *recordingStreamProvider) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{p.result}, nil
}
func (p *recordingStreamProvider) GetDetails(string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (p *recordingStreamProvider) GetSeasons(string) ([]media.Season, error) { return nil, nil }
func (p *recordingStreamProvider) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, nil
}
func (p *recordingStreamProvider) GetServers(string, string) ([]media.Server, error) {
	return nil, nil
}
func (p *recordingStreamProvider) GetEmbedURL(string) (string, error) { return "", nil }
func (p *recordingStreamProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (p *recordingStreamProvider) Recent(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (p *recordingStreamProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.lastEpisode = episodeID
	p.watchCalled = true
	p.watchCallCnt++
	p.mu.Unlock()
	return &media.Stream{URL: p.url}, nil
}

func (p *recordingStreamProvider) episodeAsked() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEpisode
}

// stubStreamServer serves 200 for anything, so the resolver's stream
// validation hop (internal/resolver/validate.go) succeeds without leaving the
// machine.
func stubStreamServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/stream.m3u8"
}

// withFallbackChain installs a fixed fallback chain for the duration of the
// test, so nothing reaches a real provider.
func withFallbackChain(t *testing.T, ps ...provider.Provider) {
	t.Helper()
	prev := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return ps }
	t.Cleanup(func() { agentFallbackProviders = prev })
}

// A primary whose season list is real but whose episode list is unavailable —
// the exact shape of MovieBox and VidNest now that they no longer invent one —
// must still play the requested episode. resolveAndPlay's episode-list failure
// is not fatal: it hands off to the fallback resolver, which reaches a
// StreamProvider through Watch with an arithmetically built episode ID and so
// never needs a list. Before this, the command died on "getting episodes".
func TestResolveAndPlayFallsBackWhenPrimaryCannotListEpisodes(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})

	sel := media.SearchResult{
		ID:    "tv/1403",
		Title: "Marvel's Agents of S.H.I.E.L.D.",
		Year:  "2013",
		Type:  media.TV,
	}

	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "1", Number: 1}},
		episodesErr: errProviderCannotList,
	}

	if err := resolveAndPlay(primary, sel, 1, 15); err != nil {
		t.Fatalf("resolveAndPlay = %v; a primary that cannot list episodes must still play via the fallback chain", err)
	}
	if got := fb.episodeAsked(); got != "1403:1:15" {
		t.Fatalf("fallback Watch asked for episode %q, want %q", got, "1403:1:15")
	}
}

// errProviderCannotList stands in for the error MovieBox and VidNest now
// return from GetEpisodes.
var errProviderCannotList = errors.New("episode listing unavailable")

// twentyTwoEpisodes is a real-shaped season list: season 1 of Marvel's Agents
// of S.H.I.E.L.D. has 22 episodes, the count the fabricating providers used to
// report as 10 and 50.
func twentyTwoEpisodes() []media.Episode {
	eps := make([]media.Episode, 0, 22)
	for n := 1; n <= 22; n++ {
		eps = append(eps, media.Episode{ID: fmt.Sprintf("f1e%d", n), Number: n})
	}
	return eps
}

// seasonSource picks a provider on its ability to enumerate seasons, which is
// not the same question as enumerating episodes: MovieBox reports a real
// season count from cached search data and cannot list episodes at all. Left
// alone, `episodes` under such a primary is exit 3 for every show. The command
// must fall through to a provider that can answer both.
func TestEpisodesFallsBackWhenSeasonSourceCannotListEpisodes(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	primary := twoSeasonStub()
	primary.episodesErr = errProviderCannotList
	withStubProvider(t, primary)

	// More than one member on purpose: with a single-provider chain the test
	// cannot tell "tries the chain" from "tries every chain provider until one
	// answers", and the first fallback in the real chain order (VidNest) is
	// itself a seasons-yes/episodes-no provider.
	withFallbackChain(t, blockedLister(), answeringLister())
	withEpisodesFlags(t, tvRef(t, ""), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v; a primary that cannot list episodes must fall through to one that can", err)
	}

	var got struct {
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 22 {
		t.Fatalf("listed %d episodes, want the fallback's 22", len(got.Episodes))
	}
}

// listingStreamProvider both lists (via stubProvider) and streams, which is
// the combination needed to observe the silent-S1E1 bug: the primary answers
// the season/episode lists and then plays whatever resolveAndPlay selected, so
// a wrong selection shows up as a successful play of the wrong episode rather
// than as an error from somewhere else.
type listingStreamProvider struct {
	*stubProvider
	url string
}

func (p *listingStreamProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	return &media.Stream{URL: p.url}, nil
}

// countingPlayer records how many times playback actually started.
type countingPlayer struct {
	stubPlayerImpl
	mu    sync.Mutex
	plays int
}

func (p *countingPlayer) Play(s *media.Stream, title string, pos float64, subs []string) (player.PlayResult, error) {
	p.mu.Lock()
	p.plays++
	p.mu.Unlock()
	return p.stubPlayerImpl.Play(s, title, pos, subs)
}

func (p *countingPlayer) played() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.plays
}

// The requested episode is not in a list the provider really did return.
// resolveAndPlay used to leave episodeIdx at its zero value and play episode
// one, reporting success — the worst outcome available, because the caller is
// told it got episode 47 of a season that has three. It must be an error.
func TestResolveAndPlayRefusesAnEpisodeTheListLacks(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)
	withNoFallbackProviders(t)

	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Type: media.TV}
	p := &listingStreamProvider{
		stubProvider: &stubProvider{
			seasons: []media.Season{{ID: "s1", Number: 1}},
			episodesBySeason: map[string][]media.Episode{
				"s1": {{ID: "e1", Number: 1}, {ID: "e2", Number: 2}, {ID: "e3", Number: 3}},
			},
		},
		url: "http://127.0.0.1:1/never-dialed.m3u8",
	}

	err := resolveAndPlay(p, sel, 1, 47)
	if err == nil {
		t.Fatalf("resolveAndPlay returned nil for episode 47 of a three-episode season; it played episode %d instead of refusing", 1)
	}
	if !strings.Contains(err.Error(), "47") {
		t.Errorf("error = %v; it must name the episode that does not exist", err)
	}
	if n := pl.played(); n != 0 {
		t.Errorf("player started %d time(s); a missing episode must never play different content", n)
	}
}

// The same hole one level up: a season number the provider's own list does not
// contain left seasonIdx at zero and played season one.
func TestResolveAndPlayRefusesASeasonTheListLacks(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)
	withNoFallbackProviders(t)

	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Type: media.TV}
	p := &listingStreamProvider{
		stubProvider: &stubProvider{
			seasons: []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}, {ID: "s3", Number: 3}},
			episodesBySeason: map[string][]media.Episode{
				"s1": {{ID: "e1", Number: 1}},
				"s2": {{ID: "e2", Number: 1}},
				"s3": {{ID: "e3", Number: 1}},
			},
		},
		url: "http://127.0.0.1:1/never-dialed.m3u8",
	}

	err := resolveAndPlay(p, sel, 5, 1)
	if err == nil {
		t.Fatal("resolveAndPlay returned nil for season 5 of a three-season show; it played season 1 instead of refusing")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error = %v; it must name the season that does not exist", err)
	}
	if n := pl.played(); n != 0 {
		t.Errorf("player started %d time(s); a missing season must never play different content", n)
	}
}

// fallbackStubProvider is a distinct Go type from stubProvider so the envelope
// can be checked to name the provider that actually answered rather than the
// configured primary — the two are indistinguishable when both are the same
// stub type.
type fallbackStubProvider struct{ *stubProvider }

// seasonSource silently re-searches the chain and returns whichever provider
// replies, and the envelope did not say which. That opacity is what made the
// fabricated episode lists invisible: a listing that looked like the primary's
// answer was another provider's. The envelope must name the answering
// provider.
func TestEpisodesNamesTheAnsweringProvider(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary cannot enumerate this ref at all, so the answer comes from
	// the chain — the case where naming it matters.
	primary := &stubProvider{seasonsErr: errProviderCannotList}
	withStubProvider(t, primary)

	fb := &fallbackStubProvider{&stubProvider{
		results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
		seasons: []media.Season{{ID: "f1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{
			"f1": {{ID: "f1e1", Number: 1, Title: "Pilot"}},
		},
	}}
	withFallbackChain(t, fb)
	withEpisodesFlags(t, tvRef(t, ""), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}

	var got struct {
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Provider != "fallbackstubprovider" {
		t.Fatalf("provider = %q, want %q — the envelope must name the provider that answered, not the configured primary", got.Provider, "fallbackstubprovider")
	}
}

// blockedListerProvider and answeringListerProvider are distinct Go types so
// providerLabel can tell them apart in the envelope — two *stubProvider values
// are indistinguishable there.
type blockedListerProvider struct{ *stubProvider }
type answeringListerProvider struct{ *stubProvider }

// blockedLister has the show and can enumerate its seasons but cannot list
// episodes — the shape of VidNest and MovieBox, and the first two entries of
// the real fallback chain.
func blockedLister() *blockedListerProvider {
	return &blockedListerProvider{&stubProvider{
		results:     []media.SearchResult{{ID: "tv/blocked-1", Title: "Some Show", Type: media.TV}},
		seasons:     []media.Season{{ID: "b1", Number: 1}},
		episodesErr: errProviderCannotList,
	}}
}

// answeringLister can answer both questions.
func answeringLister() *answeringListerProvider {
	return &answeringListerProvider{&stubProvider{
		results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
		seasons: []media.Season{{ID: "f1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{
			"f1": twentyTwoEpisodes(),
		},
	}}
}

// The chain is scanned for a provider that can enumerate *seasons*, which is a
// different question from enumerating episodes. Taking the first such hit and
// making exactly one GetEpisodes call means a show the rest of the chain could
// list exits 3 — and the real chain leads with two providers of exactly that
// shape (VidNest, MovieBox). Every hit must be tried, in chain order.
//
// Three chain members, not two, and that is load-bearing. seasonSource takes
// hits[0] as the answering provider and leaves the rest as alts, so a
// two-member chain gives firstEpisodeList a single-element slice and it never
// iterates at all — the test then passes on a build where firstEpisodeList
// only ever looks at its first hit, which is exactly the bug it is named for.
// Proven by inserting `hits = hits[:1]` into firstEpisodeList: with two
// members this test stayed green, with three it fails.
func TestEpisodesTriesLaterChainProvidersWhenTheFirstCannotListEpisodes(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary cannot enumerate seasons either, so the season list itself
	// comes from the chain — the path where the hits are already in hand.
	withStubProvider(t, &stubProvider{seasonsErr: errProviderCannotList})
	withFallbackChain(t, blockedLister(), blockedLister(), answeringLister())
	withEpisodesFlags(t, tvRef(t, ""), 1)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v; a later chain provider could list all 22 episodes", err)
	}

	var got struct {
		Provider string `json:"provider"`
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if len(got.Episodes) != 22 {
		t.Fatalf("listed %d episodes, want the second chain provider's 22", len(got.Episodes))
	}
	if got.Provider != "answeringlisterprovider" {
		t.Fatalf("provider = %q, want %q — the envelope must name the provider that actually listed the episodes", got.Provider, "answeringlisterprovider")
	}
}

// Refusing to invent a list is only defensible if the refusal tells the user
// where a real one lives. Under a MovieBox or VidNest primary the interactive
// path has no menu to offer and no requested number to hand the resolver, so
// this error is the entire user-facing outcome: it has to name the provider
// that could not answer and point at `lobster episodes --ref ...`, which asks
// every chain provider that can enumerate the season. "getting episodes:
// episode listing unavailable" named neither.
func TestResolveAndPlayErrorNamesTheProviderAndTheRemedy(t *testing.T) {
	hostileEnv(t)

	sel := media.SearchResult{
		ID:    "tv/1403",
		Title: "Marvel's Agents of S.H.I.E.L.D.",
		Year:  "2013",
		Type:  media.TV,
	}

	// No chain at all: nothing here may reach a real provider, and with no
	// episode requested resolveAndPlay must not consult one anyway.
	withFallbackChain(t)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "1", Number: 1}},
		episodesErr: errProviderCannotList,
	}

	err := resolveAndPlay(primary, sel, 1, 0)
	if err == nil {
		t.Fatalf("resolveAndPlay = nil; a primary that cannot list episodes has no menu to offer and must fail")
	}
	msg := err.Error()
	for _, want := range []string{
		"stubprovider",
		"season 1",
		sel.Title,
		errProviderCannotList.Error(),
		"lobster episodes --ref",
		"--episode N",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not mention %q; the refusal has to name who failed and what to do instead", msg, want)
		}
	}
}

// listingChainProvider is a fallback that can do everything the primary
// cannot: it has the show, enumerates its seasons, lists a real 22-episode
// season, and streams. That combination is what makes the interactive menu
// recoverable — the numbers offered are a provider's own list, not invented.
type listingChainProvider struct {
	*stubProvider
	url string

	mu          sync.Mutex
	lastEpisode string
	lastMedia   string
}

func (p *listingChainProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.lastEpisode = episodeID
	p.lastMedia = mediaID
	p.mu.Unlock()
	return &media.Stream{URL: p.url}, nil
}

func (p *listingChainProvider) episodeAsked() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEpisode
}

// mediaAsked is the ID Watch was called with. It is recorded because the
// provider-call key and the history key were once the same field: a fixture
// that only remembers the episode ID cannot tell a session calling the chain
// provider with its own ID from one calling it with the primary's.
func (p *listingChainProvider) mediaAsked() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastMedia
}

func newListingChainProvider(url string) *listingChainProvider {
	return &listingChainProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "f1", Number: 1}},
			episodesBySeason: map[string][]media.Episode{
				"f1": twentyTwoEpisodes(),
			},
		},
		url: url,
	}
}

// recordSelections installs a menu stub that records what each menu was
// offered and picks a fixed index.
func recordSelections(t *testing.T, pick int) *[][]string {
	t.Helper()
	var offered [][]string
	prev := selectItem
	selectItem = func(prompt string, items []string) (int, error) {
		offered = append(offered, items)
		return pick, nil
	}
	t.Cleanup(func() { selectItem = prev })
	return &offered
}

// Interactive use passes no --episode, so the recovery for a primary that
// cannot enumerate episodes has to produce a *list*, not a stream: without one
// there is no menu to offer and the command dies on "getting episodes". The
// chain can list — `episodes` already makes exactly this move — so the menu
// must be offered from a chain provider's real list.
func TestResolveAndPlayOffersTheChainsEpisodeListWhenNoEpisodeRequested(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	fb := newListingChainProvider(stubStreamServer(t))
	withFallbackChain(t, fb)
	// The last episode, so the session ends rather than reaching the
	// post-playback menu.
	offered := recordSelections(t, 21)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	if err := resolveAndPlay(primary, sel, 1, 0); err != nil {
		t.Fatalf("resolveAndPlay = %v; with no --episode the chain's episode list must be offered as a menu", err)
	}
	if len(*offered) != 1 {
		t.Fatalf("menus offered = %d, want 1 episode menu", len(*offered))
	}
	if got := len((*offered)[0]); got != 22 {
		t.Fatalf("episode menu offered %d entries, want the chain's 22", got)
	}
	if got := fb.episodeAsked(); got != "f1e22" {
		t.Fatalf("chain Watch asked for episode %q, want %q — the menu selection must play the episode it named", got, "f1e22")
	}
	if n := pl.played(); n != 1 {
		t.Fatalf("player started %d time(s), want 1", n)
	}
}

// The same recovery with --episode: before this the request went straight to
// the fallback *resolver*, which returns one stream and no list, so
// next/previous-episode continuation was lost for every primary that cannot
// enumerate episodes. When the chain can list, the session is built from that
// list instead — and the requested number is looked up in it, so a fallback
// still plays the episode that was asked for or none at all.
func TestResolveAndPlayKeepsPlaylistContinuityViaTheChain(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	fb := newListingChainProvider(stubStreamServer(t))
	withFallbackChain(t, fb)
	recordSelections(t, 0) // no menu should be reached

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	// The last episode, so the session ends rather than reaching the
	// post-playback menu — its existence is the point being tested.
	if err := resolveAndPlay(primary, sel, 1, 22); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if got := fb.episodeAsked(); got != "f1e22" {
		t.Fatalf("chain Watch asked for episode %q, want the listed ID %q — a session built from the real list, not an arithmetic stream lookup", got, "f1e22")
	}
}

// A chain provider's list can be real and still short of the show — a
// currently-airing season, or a season it only partly carries. Building the
// session on it and playing the nearest entry would be the silent substitution
// this whole path exists to prevent, so a requested number the list lacks must
// go to the resolver, which needs no list.
//
// What that buys is that cmd asks for exactly the number it was given: the
// resolver formats "id:season:episode" and hands it to Watch
// (tryStreamProviderFallback, internal/resolver/probe.go). It is not a promise
// that the right episode comes back. The residual risk sits one layer down, in
// backends that answer any episode number they are handed — VidNest's own
// GetEpisodes doc says exactly that — and nothing at this level can check it.
// The guarantee here is narrower and worth having: cmd never substitutes a
// number of its own.
func TestResolveAndPlayDoesNotSubstituteWhenTheChainsListIsShort(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	fb := newListingChainProvider(stubStreamServer(t))
	// A genuine but two-episode list: nothing in it is episode 15.
	fb.episodesBySeason = map[string][]media.Episode{
		"f1": {{ID: "f1e1", Number: 1}, {ID: "f1e2", Number: 2}},
	}
	withFallbackChain(t, fb)
	recordSelections(t, 0) // no menu should be reached

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	if err := resolveAndPlay(primary, sel, 1, 15); err != nil {
		t.Fatalf("resolveAndPlay = %v; a short chain list must not cost the request its resolver hop", err)
	}
	if got := fb.episodeAsked(); got != "fallback-1:1:15" {
		t.Fatalf("chain Watch asked for episode %q, want the requested %q — never an entry from the short list", got, "fallback-1:1:15")
	}
}

// A multi-season batch download asks the primary for each season's episode
// list. Under a primary that cannot produce one, every season recorded a
// synthetic failure and nothing was downloaded — while the chain could have
// listed them in full. The list lookup must make the same move `episodes` and
// the interactive menu now make.
//
// It drives batchDownloadMultiSeason, the real caller. The recovery used to be
// pinned only through seasonEpisodes, a single-season wrapper with no
// production caller at all: deleting the chain lookup from
// (*seasonLister).episodes reddened that test alone, so the recovery could
// have been removed from the path users actually take with the suite still
// green.
func TestMultiSeasonBatchDownloadsTheChainsEpisodes(t *testing.T) {
	hostileEnv(t)
	fb := multiSeasonBatchHarness(t)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}, {ID: "s3", Number: 3}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	// Every download fails (the stream URL is unreachable), so this ends at the
	// retry prompt, which hostileEnv turns into an error. Which episodes were
	// resolved is already decided by then, and is what is being measured.
	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	// One entry per episode in the chain's own lists — 3 + 1 + 2 — and no
	// entry for a season/episode pair the chain does not carry. The primary
	// can list nothing, so every one of these came from the chain.
	want := []string{
		"fallback-1:1:1", "fallback-1:1:2", "fallback-1:1:3",
		"fallback-1:2:1",
		"fallback-1:3:1", "fallback-1:3:2",
	}
	if got := fb.episodesAsked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the chain was asked to stream %v, want %v — the episodes reaching downloadSingleEpisode must be the chain's own list", got, want)
	}
}

// Season 0 is a real, reachable season number, not a spare "unspecified"
// value: consumet maps it straight from ep.Season for specials
// (internal/provider/consumet.go), and flixhqws/parser reach it through
// strconv.Atoi, which yields 0 whenever the label does not parse.
//
// pickSeason treated any want <= 0 as "give me seasons[0]", and the recovery
// callers pass a concrete season number. So a user who picked "Season 0" from
// the menu, under a primary that cannot list its episodes, silently got a
// different season's episode list with nothing saying the season had changed —
// a silent season substitution, one level above the episode substitution this
// path exists to refuse.
func TestResolveAndPlayDoesNotSubstituteAnotherSeasonForSeasonZero(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	// The chain has the show and lists a real season — but season 1, not the
	// season 0 that was asked for.
	fb := newListingChainProvider(stubStreamServer(t))
	withFallbackChain(t, fb)

	// Index 0 of the season menu is Season 0.
	offered := recordSelections(t, 0)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s0", Number: 0}, {ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	err := resolveAndPlay(primary, sel, 0, 0)
	if err == nil {
		t.Fatalf("resolveAndPlay = nil; the chain has no season 0 and its season 1 must not stand in for one")
	}
	if len(*offered) != 1 {
		t.Fatalf("menus offered = %d, want only the season menu — an episode menu here is another season's list presented as season 0", len(*offered))
	}
	if n := pl.played(); n != 0 {
		t.Fatalf("player started %d time(s); a season the chain does not have must never play another season's episode", n)
	}
}

// shortSeasonLister has the show but undercounts its seasons — VidNest's
// GetSeasons probes each season for streams and stops at the first one without
// any, so a show whose later seasons are missing from that backend is reported
// as having fewer seasons than it does.
type shortSeasonLister struct{ *stubProvider }

// fiveSeasonLister has the same show and all five of its seasons. It streams
// too, so a play that reaches it can be told apart from one that refuses.
type fiveSeasonLister struct {
	*stubProvider
	url string

	mu      sync.Mutex
	watched []string
}

func (p *fiveSeasonLister) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.watched = append(p.watched, episodeID)
	p.mu.Unlock()
	return &media.Stream{URL: p.url}, nil
}

func (p *fiveSeasonLister) episodesWatched() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.watched...)
}

func newShortSeasonLister() *shortSeasonLister {
	return &shortSeasonLister{&stubProvider{
		results: []media.SearchResult{{ID: "tv/short-1", Title: "Some Show", Type: media.TV}},
		seasons: []media.Season{{ID: "b1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{
			"b1": {{ID: "b1e1", Number: 1, Title: "Pilot"}},
		},
	}}
}

func newFiveSeasonLister() *fiveSeasonLister { return newFiveSeasonListerAt("") }

func newFiveSeasonListerAt(url string) *fiveSeasonLister {
	return &fiveSeasonLister{url: url, stubProvider: &stubProvider{
		results: []media.SearchResult{{ID: "tv/full-1", Title: "Some Show", Type: media.TV}},
		seasons: []media.Season{
			{ID: "f1", Number: 1}, {ID: "f2", Number: 2}, {ID: "f3", Number: 3},
			{ID: "f4", Number: 4}, {ID: "f5", Number: 5},
		},
		episodesBySeason: map[string][]media.Episode{
			"f5": {{ID: "f5e1", Number: 1, Title: "S5 Premiere"}},
		},
	}}
}

// The round widened "try every hit" for episodes but not for seasons: the
// season was picked out of hits[0] alone, so a first hit that undercounts
// seasons made `--season 5` exit no_results for a show a later hit lists in
// full. Seasons need the same treatment episodes got.
func TestEpisodesFindsASeasonALaterChainHitHas(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary cannot enumerate seasons, so the season list comes from the
	// chain and hits[0] is the one that undercounts.
	withStubProvider(t, &stubProvider{seasonsErr: errProviderCannotList})
	withFallbackChain(t, newShortSeasonLister(), newFiveSeasonLister())
	withEpisodesFlags(t, tvRef(t, ""), 5)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v; a later chain hit has season 5", err)
	}

	var got struct {
		Season   int `json:"season"`
		Episodes []struct {
			Title string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Season != 5 {
		t.Fatalf("season = %d, want the requested 5", got.Season)
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Title != "S5 Premiere" {
		t.Fatalf("episodes = %+v, want season 5's own list", got.Episodes)
	}
}

// Same hole with the primary as the season source: it answers "seasons", so
// the chain was never consulted at all and a season it does not carry was
// no_results even though the chain has it.
func TestEpisodesFindsASeasonThePrimaryUndercounts(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	withStubProvider(t, &stubProvider{seasons: []media.Season{{ID: "p1", Number: 1}}})
	withFallbackChain(t, newFiveSeasonLister())
	withEpisodesFlags(t, tvRef(t, ""), 5)

	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun = %v; the chain has season 5", err)
	}

	var got struct {
		Season int `json:"season"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	if got.Season != 5 {
		t.Fatalf("season = %d, want the requested 5", got.Season)
	}
}

// scanCountingChainProvider is a chain member that records how many times its
// seasons were enumerated — one per fallbackSeasonHits scan — and which
// episodes it was asked to stream, which is the only observable proof that the
// list a batch downloaded from was its own.
type scanCountingChainProvider struct {
	*stubProvider
	url string

	mu       sync.Mutex
	scans    int
	episodes []string
}

func (p *scanCountingChainProvider) GetSeasons(id string) ([]media.Season, error) {
	p.mu.Lock()
	p.scans++
	p.mu.Unlock()
	return p.stubProvider.GetSeasons(id)
}

func (p *scanCountingChainProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.episodes = append(p.episodes, episodeID)
	p.mu.Unlock()
	// The URL is unreachable on purpose: the resolver's validation hop rejects
	// it, so no download is ever started.
	return &media.Stream{URL: p.url}, nil
}

func (p *scanCountingChainProvider) scanCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scans
}

// episodesAsked is the episode IDs Watch was handed, deduplicated and sorted
// so the assertion does not depend on the resolver's retry or race order.
// tryStreamProviderFallback builds them as "<id>:<season>:<episode>"
// (internal/resolver/probe.go), so each one names the season and episode
// number the batch actually resolved.
func (p *scanCountingChainProvider) episodesAsked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	out := make([]string, 0, len(p.episodes))
	for _, e := range p.episodes {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// multiSeasonBatchHarness sets up a download-mode batch over a chain whose
// three seasons carry 3, 1 and 2 episodes — deliberately uneven, so an
// assertion on which episodes were resolved cannot be satisfied by a list of
// the wrong shape.
func multiSeasonBatchHarness(t *testing.T) *scanCountingChainProvider {
	t.Helper()

	prevCfg := cfg
	cfg = &config.Config{Quality: "1080"}
	t.Cleanup(func() { cfg = prevCfg })

	prevJSON, prevDL := flagJSON, flagDownload
	flagJSON, flagDownload = false, t.TempDir()
	t.Cleanup(func() { flagJSON, flagDownload = prevJSON, prevDL })

	fb := &scanCountingChainProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "f1", Number: 1}, {ID: "f2", Number: 2}, {ID: "f3", Number: 3}},
			episodesBySeason: map[string][]media.Episode{
				"f1": {{ID: "f1e1", Number: 1}, {ID: "f1e2", Number: 2}, {ID: "f1e3", Number: 3}},
				"f2": {{ID: "f2e1", Number: 1}},
				"f3": {{ID: "f3e1", Number: 1}, {ID: "f3e2", Number: 2}},
			},
		},
		url: "http://127.0.0.1:1/never-dialed.m3u8",
	}
	withFallbackChain(t, fb)
	return fb
}

// A multi-season batch asks for one season's episodes at a time, and each ask
// ran a full chain scan of its own: search plus GetSeasons across every
// fallback provider, bounded at 5s each. Ten seasons is ten scans — around a
// hundred seconds of scanning before the first byte — and nothing tied the
// seasons together, so a chain that answered differently between scans could
// hand each season to a different provider.
//
// One scan serves the whole batch.
func TestMultiSeasonBatchScansTheChainOnce(t *testing.T) {
	hostileEnv(t)
	fb := multiSeasonBatchHarness(t)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}, {ID: "s2", Number: 2}, {ID: "s3", Number: 3}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	// Every download fails (the stream URL is unreachable), so this ends at the
	// retry prompt, which hostileEnv turns into an error. The listing work is
	// already done by then and is what is being measured.
	_ = batchDownloadMultiSeason(primary, sel, primary.seasons)

	if got := fb.scanCount(); got != 1 {
		t.Fatalf("the chain was scanned %d times for a 3-season batch, want 1 — a 10-season batch is ten full chain scans, and nothing keeps the seasons on one provider", got)
	}
}

// The TUI's download dialog only recovers a missing episode list if cmd wires
// the hook — the tui package cannot build the chain itself. The wiring lived
// inside searchRun's browser loop, which no test ever reaches, so deleting it
// cost nothing anywhere. It is an init now, and this is what notices if it
// goes.
func TestTUIEpisodeListFallbackIsWired(t *testing.T) {
	if tui.EpisodeListFallback == nil {
		t.Fatal("tui.EpisodeListFallback is nil; the TUI download dialog has no way to recover an episode list the provider cannot give it")
	}
}

// partialListPrimary answers GetEpisodes with a short list *and* an error —
// "here is what I have, and I could not finish". It streams, so playback from
// that list would succeed and nothing downstream would notice the list was
// never complete.
type partialListPrimary struct {
	*stubProvider
	url string
}

func (p *partialListPrimary) GetEpisodes(string, string) ([]media.Episode, error) {
	return []media.Episode{{ID: "e1", Number: 1}, {ID: "e2", Number: 2}}, errProviderCannotList
}

func (p *partialListPrimary) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	return &media.Stream{URL: p.url}, nil
}

// A list that arrives with an error is not a list. The guard used to read
// `if err != nil || len(episodes) == 0`; splitting it left the error checked
// only on the way in to the chain recovery and never again, so a provider
// answering "two episodes, and an error" had those two treated as the season.
//
// That is the fabrication bug wearing a different hat: season 1 has 22
// episodes, the menu would offer 2, the playlist would end after the second,
// and the run would report success throughout. The recovery still clears err
// when the chain supplies a real list, which is the only way past this gate.
func TestResolveAndPlayRefusesAPartialListThatCameWithAnError(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	// Nothing in the chain, so neither the list recovery nor the resolver hop
	// can answer and the partial list is all that is left.
	withFallbackChain(t)
	recordSelections(t, 0)

	primary := &partialListPrimary{
		stubProvider: &stubProvider{seasons: []media.Season{{ID: "s1", Number: 1}}},
		url:          stubStreamServer(t),
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	err := resolveAndPlay(primary, sel, 1, 2)
	if err == nil {
		t.Fatal("resolveAndPlay = nil; an episode list the provider itself could not finish must not stand in for the season")
	}
	if n := pl.played(); n != 0 {
		t.Fatalf("player started %d time(s) from a list that arrived with an error", n)
	}
}

// withExcludingFallbackChain installs a chain that drops whichever provider it
// is handed, which is what the real fallbackProviders does (by concrete type,
// cmd/fallback.go) and what withFallbackChain's fixed list cannot express. The
// distinction is the whole of this pair of tests: once the episode-list
// recovery moves playback to a chain provider, handing that provider to the
// chain builder excludes the one source proven to have this work and adds back
// the primary that could not even list it.
func withExcludingFallbackChain(t *testing.T, ps ...provider.Provider) {
	t.Helper()
	prev := agentFallbackProviders
	agentFallbackProviders = func(primary provider.Provider) []provider.Provider {
		out := make([]provider.Provider, 0, len(ps))
		for _, p := range ps {
			if p != primary {
				out = append(out, p)
			}
		}
		return out
	}
	t.Cleanup(func() { agentFallbackProviders = prev })
}

// twoEpisodeChainProvider is a chain member with one season of two episodes,
// small enough that a batch over it is quick.
func twoEpisodeChainProvider(url string) *scanCountingChainProvider {
	return &scanCountingChainProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "f1", Number: 1}},
			episodesBySeason: map[string][]media.Episode{
				"f1": {{ID: "f1e1", Number: 1}, {ID: "f1e2", Number: 2}},
			},
		},
		url: url,
	}
}

// After the episode-list recovery, every later use of the rebound provider
// treated it as "the primary" — including chain construction. A batch download
// then resolved each episode from a chain that excluded the provider which had
// just listed them, and included the primary that could not.
//
// Nothing masks it here: unlike playback, downloadSingleEpisode has no
// try-the-session's-provider-first step. The chain is the only thing it has.
func TestBatchDownloadKeepsTheAnsweringProviderInItsChain(t *testing.T) {
	hostileEnv(t)

	prevCfg := cfg
	cfg = &config.Config{Quality: "1080"}
	t.Cleanup(func() { cfg = prevCfg })

	prevJSON, prevDL := flagJSON, flagDownload
	flagJSON, flagDownload = false, t.TempDir()
	t.Cleanup(func() { flagJSON, flagDownload = prevJSON, prevDL })

	fb := twoEpisodeChainProvider("http://127.0.0.1:1/never-dialed.m3u8")
	withExcludingFallbackChain(t, fb)

	// One season, so the season menu offers no batch entries and index 0 is
	// Season 1; the episode menu does, and index 0 there is "Download all".
	recordSelections(t, 0)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	// The downloads themselves fail on an unreachable URL and end at the retry
	// prompt, which hostileEnv turns into an error. Which providers were asked
	// is settled before that.
	_ = resolveAndPlay(primary, sel, 0, 0)

	want := []string{"fallback-1:1:1", "fallback-1:1:2"}
	if got := fb.episodesAsked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the provider that listed the season was asked to stream %v, want %v — the recovery must not exclude it from its own chain", got, want)
	}
}

// listedIDFailingProvider lists a season and streams it, but its own listed
// episode IDs no longer resolve — a dead entry on its side, which is the state
// AllAnime's finished series are in and any scraper can reach transiently. The
// resolver's arithmetic "<id>:<season>:<episode>" lookup still works, so the
// provider is far from useless; it just cannot be reached through the
// session's first attempt.
type listedIDFailingProvider struct {
	*stubProvider
	url string

	mu   sync.Mutex
	byID []string
}

func (p *listedIDFailingProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	if strings.HasPrefix(episodeID, "f1e") {
		p.mu.Lock()
		p.byID = append(p.byID, episodeID)
		p.mu.Unlock()
		return nil, errors.New("that episode ID is gone")
	}
	return &media.Stream{URL: p.url}, nil
}

// The session built on a recovered list carries the answering provider, and
// cmd/session.go's resolveStream used it to build the fallback chain too. The
// Watch-first step masks that whenever the provider answers to its own episode
// IDs — but when it does not, playback fell all the way through to a chain
// with the one provider that has this show removed from it.
func TestRecoveredSessionKeepsTheAnsweringProviderInItsChain(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)

	fb := &listedIDFailingProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "f1", Number: 1}},
			episodesBySeason: map[string][]media.Episode{
				"f1": {{ID: "f1e1", Number: 1}, {ID: "f1e2", Number: 2}},
			},
		},
		url: stubStreamServer(t),
	}
	withExcludingFallbackChain(t, fb)
	recordSelections(t, 0)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	}
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}

	// The last episode, so the session ends rather than reaching the
	// post-playback menu.
	if err := resolveAndPlay(primary, sel, 1, 2); err != nil {
		t.Fatalf("resolveAndPlay = %v; the provider that listed this season can still resolve it by number", err)
	}
	if n := pl.played(); n != 1 {
		t.Fatalf("player started %d time(s), want 1", n)
	}
}

// seasonSource's own doc comment states the rule: "The two commands must agree
// on what one ref means." Widening the season lookup across chain hits gave
// `episodes` a recovery that `play` did not have, so the agent workflow
// find -> episodes --season 5 -> play --season 5 --episode 1 succeeded at step
// two and exited no_results at step three, pointing the caller at the very
// command that had just listed the season.
//
// One test, both commands, one fixture pair: a primary that undercounts (the
// shape VidNest's GetSeasons has — it probes each season for streams and stops
// at the first it cannot reach) and a chain member that has the show in full.
func TestPlayAndEpisodesAgreeOnASeasonThePrimaryUndercounts(t *testing.T) {
	hostileEnv(t)
	pl := &countingPlayer{stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}}}
	playStreamHarness(t, pl)
	buf := captureAgentOut(t)

	fb := newFiveSeasonListerAt(stubStreamServer(t))
	withFallbackChain(t, fb)
	// The primary has the ref and answers about it — it is simply missing the
	// later seasons, which is why nothing on the play side ever consulted the
	// chain about them.
	primary := &stubProvider{seasons: []media.Season{{ID: "p1", Number: 1}}}
	withStubProvider(t, primary)

	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	ref := tvRef(t, "")

	withEpisodesFlags(t, ref, 5)
	episodesErr := episodesRun(episodesCmd, nil)
	var listed struct {
		Season   int `json:"season"`
		Episodes []struct {
			Number int `json:"number"`
		} `json:"episodes"`
	}
	if episodesErr == nil {
		if err := json.Unmarshal(buf.Bytes(), &listed); err != nil {
			t.Fatalf("bad JSON from episodes: %v (%q)", err, buf.String())
		}
	}

	prevEpisode := flagEpisode
	flagEpisode = 1
	t.Cleanup(func() { flagEpisode = prevEpisode })
	buf.Reset()
	playErr := playRun(playCmd, nil)

	// The agreement itself. Either both commands have season 5 or neither
	// does; "episodes lists it, play refuses it" is the state that breaks the
	// workflow, whichever way round.
	if (episodesErr == nil) != (playErr == nil) {
		t.Fatalf("episodes = %v but play = %v; the two commands must agree on what one ref means", episodesErr, playErr)
	}
	// And the answer they agree on has to be the true one: the chain really
	// does have season 5, so agreeing to refuse it would be the wrong half of
	// the recovery deleted rather than the missing half added.
	if episodesErr != nil {
		t.Fatalf("neither command found season 5, which a chain member lists in full: %v", episodesErr)
	}
	if listed.Season != 5 || len(listed.Episodes) != 1 || listed.Episodes[0].Number != 1 {
		t.Fatalf("episodes listed season %d with %+v, want season 5 episode 1", listed.Season, listed.Episodes)
	}
	if got := fb.episodesWatched(); len(got) != 1 || got[0] != "f5e1" {
		t.Fatalf("the chain was asked to stream %v, want the season 5 episode it listed (f5e1)", got)
	}
	if n := pl.played(); n != 1 {
		t.Fatalf("player started %d time(s), want 1 — the episode episodes had just listed", n)
	}
}
