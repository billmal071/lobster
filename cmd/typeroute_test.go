package cmd

import (
	"sync"
	"testing"
	"time"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

// ytsCatalogStub stands in for the YTS provider: it answers Search from a
// canned catalogue and records every call, so a test can assert that the route
// did NOT consult it as easily as that it did. It is a StreamProvider because
// the real YTS is one, and resolveAndPlay branches on that.
type ytsCatalogStub struct {
	mu       sync.Mutex
	results  []media.SearchResult
	searches int
	block    time.Duration

	lastWatchID string
	stream      *media.Stream
}

func (y *ytsCatalogStub) Search(string) ([]media.SearchResult, error) {
	y.mu.Lock()
	y.searches++
	block := y.block
	res := y.results
	y.mu.Unlock()
	if block > 0 {
		time.Sleep(block)
	}
	return res, nil
}
func (y *ytsCatalogStub) searchCount() int {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.searches
}
func (y *ytsCatalogStub) GetDetails(string) (*media.ContentDetail, error) { return nil, nil }
func (y *ytsCatalogStub) GetSeasons(string) ([]media.Season, error)       { return nil, nil }
func (y *ytsCatalogStub) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, nil
}
func (y *ytsCatalogStub) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{ID: "torrent-2160p", Name: "2160p (42 seeds)"}}, nil
}
func (y *ytsCatalogStub) GetEmbedURL(string) (string, error) { return "", nil }
func (y *ytsCatalogStub) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (y *ytsCatalogStub) Recent(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (y *ytsCatalogStub) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	y.mu.Lock()
	y.lastWatchID = mediaID
	y.mu.Unlock()
	return y.stream, nil
}

// withYTSRoute installs a stubbed YTS behind the route's seam and a cfg whose
// Base is b.
func withYTSRoute(t *testing.T, b string, stub *ytsCatalogStub) {
	t.Helper()
	withBase(t, b)
	prev := newYTSProvider
	newYTSProvider = func() provider.Provider { return stub }
	t.Cleanup(func() { newYTSProvider = prev })

	prevDL := flagDownload
	flagDownload = ""
	t.Cleanup(func() { flagDownload = prevDL })
}

func matrixOnYTS() *ytsCatalogStub {
	return &ytsCatalogStub{results: []media.SearchResult{
		{ID: "yts/1745", Title: "The Matrix", Year: "1999", Type: media.Movie},
	}}
}

// The owner's ask: `lobster "the matrix"` should end up on YTS without anyone
// passing a flag. The selection arrives carrying a scraper's ID, and YTS
// rejects a foreign ID outright (movieByID validates "yts/<numeric>"), so the
// route has to hand back the YTS ID for the same film — not merely the YTS
// provider.
func TestRouteByTypeSendsAMovieToYTSUnderAutoBase(t *testing.T) {
	yts := matrixOnYTS()
	withYTSRoute(t, "auto", yts)

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	got, routed := routeByType(primary, sel)
	if got != provider.Provider(yts) {
		t.Fatalf("routeByType returned %T, want the YTS provider", got)
	}
	if routed.ID != "yts/1745" {
		t.Fatalf("routed selection ID = %q, want yts/1745 — YTS cannot answer about a foreign ID", routed.ID)
	}
	if routed.Title != "The Matrix" || routed.Year != "1999" {
		t.Fatalf("routed selection = %q (%s), want the title and year the user picked", routed.Title, routed.Year)
	}
}

// YTS is movies-only: asked for a series it answers "no seasons found"
// (measured 2026-09-09 against Marvel's Agents of S.H.I.E.L.D.). So a series
// must never be routed to it — and, because the route is what would send it
// there, it must not even ask YTS about one.
func TestRouteByTypeNeverSendsASeriesToYTS(t *testing.T) {
	yts := matrixOnYTS()
	// A series whose title YTS *would* match if it were asked, so the test
	// fails on the routing rule rather than on a lookup that missed.
	yts.results = []media.SearchResult{
		{ID: "yts/9001", Title: "Agents of S.H.I.E.L.D.", Year: "2013", Type: media.Movie},
	}
	withYTSRoute(t, "auto", yts)

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "tv/1403", Title: "Agents of S.H.I.E.L.D.", Year: "2013", Type: media.TV}

	got, routed := routeByType(primary, sel)
	if got != provider.Provider(primary) {
		t.Fatalf("routeByType sent a series to %T, want the provider that found it", got)
	}
	if routed.ID != "tv/1403" {
		t.Fatalf("routed series ID = %q, want the untouched tv/1403", routed.ID)
	}
	if n := yts.searchCount(); n != 0 {
		t.Fatalf("YTS was searched %d times for a series; it has no TV catalogue and must not be consulted", n)
	}
}

// The other half of the same rule: when YTS is already the primary — reached
// under auto, e.g. a movie routed there and then a series selected in the same
// TUI session — a series must be moved off it, not left to fail.
func TestRouteByTypeMovesASeriesOffAYTSPrimary(t *testing.T) {
	withYTSRoute(t, "auto", matrixOnYTS())

	sel := media.SearchResult{ID: "tv/1403", Title: "Agents of S.H.I.E.L.D.", Type: media.TV}
	got, _ := routeByType(provider.NewYTS(), sel)
	if _, isYTS := got.(*provider.YTS); isYTS {
		t.Fatalf("routeByType left a series on *provider.YTS, which cannot enumerate seasons")
	}
	if _, ok := got.(*provider.Soap2Day); !ok {
		t.Fatalf("routeByType moved a series to %T, want the auto base's general source", got)
	}
}

// `--base X` (and `base = "X"` in config.toml) is a deliberate choice and must
// win for either type. The input that would violate the guarantee is a movie
// under an explicit non-YTS base: that is exactly what the auto rule would
// otherwise redirect.
func TestRouteByTypeLeavesAnExplicitBaseAlone(t *testing.T) {
	yts := matrixOnYTS()
	withYTSRoute(t, "soap2day", yts)

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	got, routed := routeByType(primary, sel)
	if got != provider.Provider(primary) {
		t.Fatalf("routeByType overrode an explicit base with %T", got)
	}
	if routed.ID != "movie/the-matrix-19" {
		t.Fatalf("routed ID = %q under an explicit base, want the selection untouched", routed.ID)
	}
	if n := yts.searchCount(); n != 0 {
		t.Fatalf("YTS was searched %d times under an explicit base; the user's choice must not be second-guessed", n)
	}
}

// A title YTS does not carry must keep playing from wherever it was found.
// The input that would violate this is YTS answering about a *different* film,
// not answering emptily: resolver.Candidates applies no score threshold, so
// without the resolver.Matches gate this near-miss would be played under the
// selected title and nothing downstream could detect the swap.
func TestRouteByTypeKeepsThePrimaryWhenYTSAnswersAboutADifferentFilm(t *testing.T) {
	yts := &ytsCatalogStub{results: []media.SearchResult{
		{ID: "yts/2222", Title: "The Matrix Reloaded", Year: "2003", Type: media.Movie},
		{ID: "yts/3333", Title: "The Matrix Revolutions", Year: "2003", Type: media.Movie},
	}}
	withYTSRoute(t, "auto", yts)

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	got, routed := routeByType(primary, sel)
	if got != provider.Provider(primary) {
		t.Fatalf("routeByType routed to %T on a near miss; YTS offered the sequels, not the film", got)
	}
	if routed.ID != "movie/the-matrix-19" {
		t.Fatalf("routed ID = %q, want the selection untouched when YTS has no match", routed.ID)
	}
}

// find, episodes and play exist so an agent is never left waiting, and
// `play --ref` reaches this route. A YTS that never answers must cost the
// route its deadline and no more, then fall back to the primary.
func TestRouteByTypeGivesUpOnAHangingYTSAndKeepsThePrimary(t *testing.T) {
	yts := matrixOnYTS()
	yts.block = 30 * time.Second
	withYTSRoute(t, "auto", yts)

	prevTimeout := ytsRouteTimeout
	ytsRouteTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ytsRouteTimeout = prevTimeout })

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	start := time.Now()
	got, routed := routeByType(primary, sel)
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("routeByType blocked for %v on a hanging YTS; it must give up at its deadline", elapsed)
	}
	if got != provider.Provider(primary) {
		t.Fatalf("routeByType returned %T after the lookup timed out, want the primary", got)
	}
	if routed.ID != "movie/the-matrix-19" {
		t.Fatalf("routed ID = %q after a timeout, want the selection untouched", routed.ID)
	}
}

// YTS resolves to a magnet, and the download path refuses a magnet outright
// (streamToResultChecked). Routing a movie download to YTS would turn a
// working download into "this source is a torrent".
func TestRouteByTypeDoesNotSendADownloadToYTS(t *testing.T) {
	yts := matrixOnYTS()
	withYTSRoute(t, "auto", yts)
	flagDownload = t.TempDir()

	primary := &stubProvider{}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	got, _ := routeByType(primary, sel)
	if got != provider.Provider(primary) {
		t.Fatalf("routeByType sent a download to %T; a magnet cannot be downloaded this way", got)
	}
}

// A Live TV channel's SearchResult is typed media.Movie
// (internal/provider/livetv.go, channelResult), so without an explicit guard
// the route would look the channel up in a film catalogue and play a film in
// its place.
func TestRouteByTypeLeavesALiveChannelAlone(t *testing.T) {
	yts := &ytsCatalogStub{results: []media.SearchResult{
		{ID: "yts/4444", Title: "BBC News", Year: "2011", Type: media.Movie},
	}}
	withYTSRoute(t, "auto", yts)

	live := provider.NewLiveTV(nil)
	sel := media.SearchResult{ID: "live/bbc-news", Title: "BBC News"}

	got, routed := routeByType(live, sel)
	if got != provider.Provider(live) {
		t.Fatalf("routeByType moved a live channel to %T", got)
	}
	if routed.ID != "live/bbc-news" {
		t.Fatalf("routed live ID = %q, want it untouched", routed.ID)
	}
	if n := yts.searchCount(); n != 0 {
		t.Fatalf("YTS was searched %d times for a live channel", n)
	}
}

// scraperStreamStub is the primary a movie arrives from: a StreamProvider that
// can resolve its own stream, so an unrouted resolveAndPlay plays ITS url and
// never reaches the real fallback chain. (An stubStreamProvider with no
// servers would send resolveAndPlay to tryFallbackStream, which talks to live
// providers — a test must never do that.)
type scraperStreamStub struct{ stream *media.Stream }

func (s *scraperStreamStub) Search(string) ([]media.SearchResult, error)     { return nil, nil }
func (s *scraperStreamStub) GetDetails(string) (*media.ContentDetail, error) { return nil, nil }
func (s *scraperStreamStub) GetSeasons(string) ([]media.Season, error)       { return nil, nil }
func (s *scraperStreamStub) GetEpisodes(string, string) ([]media.Episode, error) {
	return nil, nil
}
func (s *scraperStreamStub) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{ID: "srv1", Name: "scraper"}}, nil
}
func (s *scraperStreamStub) GetEmbedURL(string) (string, error) { return "", nil }
func (s *scraperStreamStub) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (s *scraperStreamStub) Recent(media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
func (s *scraperStreamStub) Watch(string, string, string, string) (*media.Stream, error) {
	return s.stream, nil
}

// recordingPlayer captures the stream that actually reached the player.
type recordingPlayer struct{ url string }

func (r *recordingPlayer) Play(s *media.Stream, _ string, _ float64, _ []string) (player.PlayResult, error) {
	r.url = s.URL
	return player.PlayResult{}, nil
}
func (r *recordingPlayer) Name() string    { return "stub" }
func (r *recordingPlayer) Available() bool { return true }

// The routing has to sit on the funnel every selection passes through, because
// one interactive search returns movies and series interleaved and the type is
// not known until a row is picked. resolveAndPlay is that funnel — the
// interactive picker, the TUI and `play --ref` all reach it — so a movie
// handed to it with a scraper primary must still come out of YTS.
//
// This drives the real resolveAndPlay rather than routeByType directly, so a
// route that is correct but unwired reds here.
func TestResolveAndPlayRoutesAMovieToYTSAtSelectionTime(t *testing.T) {
	rec := &recordingPlayer{}
	playStreamHarness(t, rec)
	cfg.Base = "auto"
	cfg.Quality = "1080"

	yts := matrixOnYTS()
	yts.stream = &media.Stream{URL: "http://127.0.0.1:1/yts-2160p.mkv"}
	prev := newYTSProvider
	newYTSProvider = func() provider.Provider { return yts }
	t.Cleanup(func() { newYTSProvider = prev })

	scraper := &scraperStreamStub{stream: &media.Stream{URL: "http://127.0.0.1:1/scraper-1080p.m3u8"}}
	sel := media.SearchResult{ID: "movie/the-matrix-19", Title: "The Matrix", Year: "1999", Type: media.Movie}

	if err := resolveAndPlay(scraper, sel, 0, 0); err != nil {
		t.Fatalf("resolveAndPlay: %v", err)
	}
	if rec.url != "http://127.0.0.1:1/yts-2160p.mkv" {
		t.Fatalf("player received %q, want the YTS stream — resolveAndPlay did not route by type", rec.url)
	}
	yts.mu.Lock()
	watched := yts.lastWatchID
	yts.mu.Unlock()
	if watched != "yts/1745" {
		t.Fatalf("YTS was asked to watch %q, want yts/1745 — a foreign ID would be rejected", watched)
	}
}
