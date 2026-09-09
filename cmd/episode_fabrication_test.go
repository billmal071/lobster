package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	fb := &stubProvider{
		results: []media.SearchResult{{ID: "tv/fallback-1", Title: "Some Show", Type: media.TV}},
		seasons: []media.Season{{ID: "f1", Number: 1}},
		episodesBySeason: map[string][]media.Episode{
			"f1": twentyTwoEpisodes(),
		},
	}
	withFallbackChain(t, fb)
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
