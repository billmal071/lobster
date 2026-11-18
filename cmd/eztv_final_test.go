package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

func chainOf(t *testing.T, ps ...provider.Provider) {
	t.Helper()
	prev := agentFallbackProviders
	agentFallbackProviders = func(provider.Provider) []provider.Provider { return ps }
	t.Cleanup(func() { agentFallbackProviders = prev })
}

func chainShow() *stubProvider {
	fb := twoSeasonStub()
	fb.results = []media.SearchResult{{ID: "fb/some-show", Title: "Some Show", Type: media.TV}}
	return fb
}

func runEpisodesUnderEZTV(t *testing.T, primary *stubProvider, season int) []episodesWarning {
	t.Helper()
	buf := captureAgentOut(t)
	withBase(t, "eztv")
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")
	withEpisodesFlags(t, tvRef(t, ""), season)
	if err := episodesRun(episodesCmd, nil); err != nil {
		t.Fatalf("episodesRun: %v", err)
	}
	return decodeEpisodesWarnings(t, buf.Bytes())
}

func fromFallback(t *testing.T, ws []episodesWarning) episodesWarning {
	t.Helper()
	for _, w := range ws {
		if w.Code == "episode_list_from_fallback" {
			return w
		}
	}
	t.Fatalf("warnings = %+v, want an episode_list_from_fallback", ws)
	return episodesWarning{}
}

func TestSeasonMissingFromATransientlyShortListMakesNoEZTVClaim(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainShow())
	primary := &stubProvider{
		seasons:    []media.Season{{ID: "1", Number: 1}},
		seasonsErr: fmt.Errorf("%w: page 2 failed", provider.ErrIncompleteEpisodeList),
	}
	w := fromFallback(t, runEpisodesUnderEZTV(t, primary, 2))
	if strings.Contains(w.Message, "will not play via EZTV") {
		t.Errorf("message = %q: the list was cut short by a failed page, so EZTV may have the season", w.Message)
	}
}

func TestSeasonMissingFromAGappedListSaysItWillNotPlayViaEZTV(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainShow())
	primary := &stubProvider{
		seasons:    []media.Season{{ID: "3", Number: 3}},
		seasonsErr: gapErr(),
	}
	w := fromFallback(t, runEpisodesUnderEZTV(t, primary, 1))
	if !strings.Contains(w.Message, "will not play via EZTV") {
		t.Errorf("message = %q, want the will-not-play-via-EZTV wording: EZTV listed its whole catalogue and season 1 is not in it", w.Message)
	}
}

func TestSeasonListWithNothingAndNoErrorSaysItWillNotPlayViaEZTV(t *testing.T) {
	hostileEnv(t)
	chainOf(t, chainShow())
	w := fromFallback(t, runEpisodesUnderEZTV(t, &stubProvider{}, 2))
	if !strings.Contains(w.Message, "will not play via EZTV") {
		t.Errorf("message = %q, want an empty answer from EZTV read as 'not here'", w.Message)
	}
}

func TestSeasonAbsentFromAGappedListIsNoResultsNotProvidersFailed(t *testing.T) {
	hostileEnv(t)
	chainOf(t)
	buf := captureAgentOut(t)
	withBase(t, "eztv")
	primary := &stubProvider{seasons: []media.Season{{ID: "3", Number: 3}}, seasonsErr: gapErr()}
	withStubProvider(t, primary)
	withStubProviderBase(t, primary, "eztv")
	withEpisodesFlags(t, tvRef(t, ""), 1)

	err := episodesRun(episodesCmd, nil)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitNoResults {
		t.Fatalf("err = %v, want exit %d: EZTV fetched its whole list and season 1 is not in it", err, exitNoResults)
	}
	if !strings.Contains(buf.String(), "EZTV does not carry season 1") {
		t.Errorf("output = %s, want it to say EZTV does not carry season 1", buf.String())
	}
}

func TestValidateRefusesASeasonAbsentFromAGappedList(t *testing.T) {
	hostileEnv(t)
	chainOf(t)
	buf := captureAgentOut(t)
	primary := &stubProvider{seasons: []media.Season{{ID: "3", Number: 3}}, seasonsErr: gapErr()}
	withStubProviderBase(t, primary, "eztv")
	err := validateSeasonEpisode(primary, playRef{ID: "eztv/1", Title: "Show", Type: "tv"}, 1, 1)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != exitNoResults {
		t.Fatalf("err = %v, want exit %d for a season missing from a gapped season list", err, exitNoResults)
	}
	if !strings.Contains(buf.String(), "EZTV does not carry season 1") {
		t.Errorf("output = %s, want the refusal to name EZTV as the source that lacks the season", buf.String())
	}
}

func TestBatchRejectsAPrefixFromAProbeFailure(t *testing.T) {
	hostileEnv(t)
	chainOf(t)
	primary := &stubProvider{
		seasons:         []media.Season{{ID: "1", Number: 1}},
		episodesWithErr: []media.Episode{{ID: "1x1", Number: 1}},
		episodesErr:     fmt.Errorf("%w: probe 429", provider.ErrIncompleteEpisodeList),
	}
	l := newSeasonLister(primary, media.SearchResult{ID: "x", Title: "Show", Type: media.TV})
	if eps, err := l.episodes(media.Season{ID: "1", Number: 1}); err == nil {
		t.Fatalf("accepted a 1-episode prefix (%d episodes) from a probe failure without comment", len(eps))
	}
}

func eztvPrimaryFromBase(t *testing.T, torrents string) (*provider.EZTV, *int32) {
	t.Helper()
	var apiHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/multi":
			fmt.Fprint(w, `{"results":[{"id":1,"name":"Kyle XY","media_type":"tv"}]}`)
		case strings.HasPrefix(r.URL.Path, "/tv/"):
			fmt.Fprint(w, `imdb.com/title/tt0756509`)
		case r.URL.Path == "/api/get-torrents" && r.URL.Query().Get("imdb_id") == "0756509":
			atomic.AddInt32(&apiHits, 1)
			fmt.Fprint(w, torrents)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	withBase(t, "eztv")
	p, ok := newProvider().(*provider.EZTV)
	if !ok {
		t.Fatalf("newProvider for base eztv = %T", newProvider())
	}
	p.SetBaseURLs(srv.URL, srv.URL)
	return p, &apiHits
}

func TestEZTVBaseIsBuiltAsPrimary(t *testing.T) {
	p, apiHits := eztvPrimaryFromBase(t, `{"torrents_count":0,"limit":100,"page":1}`)
	res, err := p.Search("kyle xy")
	if !errors.Is(err, provider.ErrNoResults) {
		t.Fatalf("Search = %+v, %v; want ErrNoResults for a show EZTV has no torrents for", res, err)
	}
	if atomic.LoadInt32(apiHits) == 0 {
		t.Fatal("Search never asked EZTV for the show's torrents, so it was not probing as a primary")
	}
}

func TestEZTVBaseKeepsAShowThatHasTorrents(t *testing.T) {
	body := `{"torrents_count":1,"limit":100,"page":1,"torrents":[{"id":1,"filename":"Kyle.XY.S01E01.mkv","magnet_url":"magnet:?xt=urn:btih:x","title":"Kyle XY S01E01","season":"1","episode":"1","seeds":5,"size_bytes":1}]}`
	p, apiHits := eztvPrimaryFromBase(t, body)
	res, err := p.Search("kyle xy")
	if err != nil || len(res) != 1 || res[0].ID != "eztv/0756509" {
		t.Fatalf("Search = %+v, %v; want the show EZTV holds a torrent for", res, err)
	}
	if atomic.LoadInt32(apiHits) == 0 {
		t.Fatal("Search never probed EZTV")
	}
}

func TestResolveAndPlayWarnsOfGapsInAKeptList(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{})
	withFallbackChain(t)
	warnings := captureWarnings(t)

	primary := &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}}
	sel := media.SearchResult{ID: "eztv/1", Title: "South Park", Type: media.TV}
	if err := resolveAndPlay(primary, sel, 5, 5); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if !containsSubstring(*warnings, "has no release for some episodes") {
		t.Fatalf("warnings = %v, want the gap wording, not the enumeration-shortfall one", *warnings)
	}
}

func TestResolveAndPlayWarnsOfGapsInAFallbackList(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{})
	warnings := captureWarnings(t)

	primary := &stubProvider{
		seasons:     []media.Season{{ID: "5", Number: 5}},
		episodesErr: errors.New("episodes: status 502"),
	}
	fb := &shortListStreamProvider{stubProvider: &stubProvider{
		results:         []media.SearchResult{{ID: "fb/south-park", Title: "South Park", Type: media.TV}},
		seasons:         []media.Season{{ID: "5", Number: 5}},
		episodesWithErr: []media.Episode{{ID: "5x1", Number: 1}, {ID: "5x5", Number: 5}},
		episodesErr:     gapErr(),
	}}
	withFallbackChain(t, fb)

	sel := media.SearchResult{ID: "prim/1", Title: "South Park", Type: media.TV}
	if err := resolveAndPlay(primary, sel, 5, 5); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if !containsSubstring(*warnings, "has no release for some episodes") {
		t.Fatalf("warnings = %v, want the gap warning for the fallback's list", *warnings)
	}
}
