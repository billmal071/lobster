package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
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
func TestEpisodesTriesLaterChainProvidersWhenTheFirstCannotListEpisodes(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// The primary cannot enumerate seasons either, so the season list itself
	// comes from the chain — the path where the hits are already in hand.
	withStubProvider(t, &stubProvider{seasonsErr: errProviderCannotList})
	withFallbackChain(t, blockedLister(), answeringLister())
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

// listingChainProvider is a fallback that can do everything the primary
// cannot: it has the show, enumerates its seasons, lists a real 22-episode
// season, and streams. That combination is what makes the interactive menu
// recoverable — the numbers offered are a provider's own list, not invented.
type listingChainProvider struct {
	*stubProvider
	url string

	mu          sync.Mutex
	lastEpisode string
}

func (p *listingChainProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.lastEpisode = episodeID
	p.mu.Unlock()
	return &media.Stream{URL: p.url}, nil
}

func (p *listingChainProvider) episodeAsked() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastEpisode
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
// session on it and playing the nearest entry would be the silent
// substitution this whole path exists to prevent, so a requested number the
// list lacks must go to the resolver, which needs no list, and must never play
// a different episode.
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
