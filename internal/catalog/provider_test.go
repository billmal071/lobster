package catalog

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type fakeInner struct {
	provider.Provider
	trendingCalls, recentCalls, detailCalls int
	trending, recent                        []media.SearchResult
	err                                     error
}

func (f *fakeInner) Trending(media.MediaType) ([]media.SearchResult, error) {
	f.trendingCalls++
	return f.trending, f.err
}

func (f *fakeInner) Recent(media.MediaType) ([]media.SearchResult, error) {
	f.recentCalls++
	return f.recent, f.err
}

func (f *fakeInner) GetDetails(id string) (*media.ContentDetail, error) {
	f.detailCalls++
	return &media.ContentDetail{Description: "from provider"}, nil
}

var providerRow = []media.SearchResult{{ID: "movie/slug-1", Title: "From Provider"}}

func okServer(t *testing.T) *Client {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/discover/tv":
			io.WriteString(w, `{"results":[{"id":37854,"name":"One Piece","first_air_date":"1999-10-20"}]}`)
		case strings.HasPrefix(r.URL.Path, "/movie/603"):
			io.WriteString(w, `{"id":603,"overview":"A hacker.","external_ids":{"imdb_id":"tt0133093"}}`)
		default:
			io.WriteString(w, `{"results":[{"id":603,"title":"The Matrix","release_date":"1999-03-30"}]}`)
		}
	})
	return c
}

func TestCatalogueListsReplaceProviderLists(t *testing.T) {
	inner := &fakeInner{trending: providerRow, recent: providerRow}
	p := Wrap(inner, okServer(t), General)

	got, err := p.Trending(media.Movie)
	if err != nil || len(got) != 1 || got[0].ID != "movie/603" {
		t.Fatalf("Trending = %v, %v", got, err)
	}
	got, err = p.Recent(media.Movie)
	if err != nil || len(got) != 1 || got[0].ID != "movie/603" {
		t.Fatalf("Recent = %v, %v", got, err)
	}
	if inner.trendingCalls+inner.recentCalls != 0 {
		t.Error("provider was called although the catalogue answered")
	}
	if n := p.TakeNotice(); n != "" {
		t.Errorf("notice = %q on success", n)
	}
}

func TestAnimeTabIsNonEmptyWithCatalogue(t *testing.T) {
	inner := &fakeInner{}
	p := Wrap(inner, okServer(t), Anime)
	got, err := p.Trending(media.TV)
	if err != nil || len(got) != 1 || got[0].Title != "One Piece" || got[0].ID != "tv/37854" {
		t.Fatalf("anime Trending = %v, %v", got, err)
	}
	if inner.trendingCalls != 0 {
		t.Error("anime catalogue should not consult the provider")
	}
}

func TestAnimeRecentStaysWithProvider(t *testing.T) {
	inner := &fakeInner{recent: providerRow}
	p := Wrap(inner, okServer(t), Anime)
	got, _ := p.Recent(media.TV)
	if len(got) != 1 || got[0].Title != "From Provider" || inner.recentCalls != 1 {
		t.Errorf("anime Recent = %v (provider calls %d)", got, inner.recentCalls)
	}
}

func TestNoKeyCallsOnlyTheProvider(t *testing.T) {
	inner := &fakeInner{trending: providerRow, recent: providerRow}
	p := Wrap(inner, nil, General)
	got, err := p.Trending(media.Movie)
	if err != nil || len(got) != 1 || got[0].Title != "From Provider" {
		t.Fatalf("Trending = %v, %v", got, err)
	}
	p.Recent(media.Movie)
	if inner.trendingCalls != 1 || inner.recentCalls != 1 {
		t.Errorf("provider calls = %d/%d, want 1/1", inner.trendingCalls, inner.recentCalls)
	}
}

func TestNoKeyHintShownOnce(t *testing.T) {
	p := Wrap(&fakeInner{}, nil, General)
	p.Trending(media.Movie)
	first := p.TakeNotice()
	if !strings.Contains(first, "tmdb_api_key") {
		t.Fatalf("hint = %q, want a mention of tmdb_api_key", first)
	}
	if p.TakeNotice() != "" {
		t.Error("notice not cleared after being taken")
	}
	p.Trending(media.Movie)
	p.Recent(media.Movie)
	if n := p.TakeNotice(); n != "" {
		t.Errorf("hint repeated: %q", n)
	}
}

func TestAnimeWrapperNeverHints(t *testing.T) {
	p := Wrap(&fakeInner{}, nil, Anime)
	p.Trending(media.TV)
	if n := p.TakeNotice(); n != "" {
		t.Errorf("anime wrapper hinted: %q", n)
	}
}

func TestKeyedClientNeverHints(t *testing.T) {
	p := Wrap(&fakeInner{trending: providerRow}, okServer(t), General)
	p.Trending(media.Movie)
	if n := p.TakeNotice(); n != "" {
		t.Errorf("hint shown although a key is set: %q", n)
	}
}

func failingClient(t *testing.T, status int) *Client {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
	return c
}

func TestFailuresFallBackToProviderWithANotice(t *testing.T) {
	cases := []struct {
		name   string
		client func(t *testing.T) *Client
		want   string
	}{
		{"401", func(t *testing.T) *Client { return failingClient(t, 401) }, "tmdb_api_key rejected"},
		{"429", func(t *testing.T) *Client { return failingClient(t, 429) }, "rate limit"},
		{"500", func(t *testing.T) *Client { return failingClient(t, 500) }, "HTTP 500"},
		{"network", func(t *testing.T) *Client {
			c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
			srv.Close()
			return c
		}, "TMDB request failed"},
		{"timeout", func(t *testing.T) *Client {
			c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) })
			hc := *srv.Client()
			hc.Timeout = 50 * time.Millisecond
			c.http = &hc
			return c
		}, "TMDB request failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeInner{trending: providerRow}
			p := Wrap(inner, tc.client(t), General)
			got, err := p.Trending(media.Movie)
			if err != nil || len(got) != 1 || got[0].Title != "From Provider" {
				t.Fatalf("Trending = %v, %v; want the provider list", got, err)
			}
			n := p.TakeNotice()
			if !strings.Contains(n, tc.want) {
				t.Errorf("notice = %q, want it to contain %q", n, tc.want)
			}
			if strings.Contains(n, testKey) {
				t.Errorf("notice leaks the key: %q", n)
			}
		})
	}
}

func TestFallbackErrorIsNotAnEmptyList(t *testing.T) {
	down := errors.New("provider down")
	inner := &fakeInner{err: down}
	p := Wrap(inner, failingClient(t, 429), General)
	got, err := p.Trending(media.Movie)
	if !errors.Is(err, down) || len(got) != 0 {
		t.Fatalf("Trending = %v, %v; want the provider's error", got, err)
	}
	if inner.trendingCalls != 1 {
		t.Fatalf("provider asked %d times, want 1", inner.trendingCalls)
	}
}

func TestNoFallbackNoticeWhenTheProviderListFailsToo(t *testing.T) {
	inner := &fakeInner{err: errors.New("provider down")}
	p := Wrap(inner, failingClient(t, 429), General)
	if _, notice, err := p.TrendingWithNotice(media.Movie); err == nil || notice != "" {
		t.Fatalf("TrendingWithNotice notice = %q, err = %v; want no list-fallback claim", notice, err)
	}
	if _, err := p.Recent(media.Movie); err == nil {
		t.Fatal("Recent returned no error")
	}
	if n := p.TakeNotice(); n != "" {
		t.Fatalf("Recent queued %q although no provider list was shown", n)
	}
}

func TestDetailsRouteByIDShape(t *testing.T) {
	inner := &fakeInner{}
	p := Wrap(inner, okServer(t), General)

	d, err := p.GetDetails("movie/603")
	if err != nil || !strings.Contains(d.Description, "A hacker.") || !strings.Contains(d.Description, "tt0133093") {
		t.Fatalf("catalogue detail = %+v, %v", d, err)
	}
	if inner.detailCalls != 0 {
		t.Error("provider consulted for a catalogue id")
	}

	d, err = p.GetDetails("movie/free-the-exorcist-hd-75043")
	if err != nil || d.Description != "from provider" || inner.detailCalls != 1 {
		t.Fatalf("slug detail = %+v, %v (calls %d)", d, err, inner.detailCalls)
	}
}

func TestDetailsWithoutKeyGoToProvider(t *testing.T) {
	inner := &fakeInner{}
	p := Wrap(inner, nil, General)
	if _, err := p.GetDetails("movie/603"); err != nil || inner.detailCalls != 1 {
		t.Errorf("err = %v, calls = %d", err, inner.detailCalls)
	}
}

func TestDetailsFailureFallsBackToProvider(t *testing.T) {
	inner := &fakeInner{}
	p := Wrap(inner, failingClient(t, 401), General)
	d, err := p.GetDetails("movie/603")
	if err != nil || d.Description != "from provider" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
}

func TestUnwrapReturnsInner(t *testing.T) {
	inner := &fakeInner{}
	if got := Wrap(inner, nil, General).Unwrap(); got != provider.Provider(inner) {
		t.Error("Unwrap did not return the wrapped provider")
	}
}

type showProvider struct {
	provider.Provider
	results    []media.SearchResult
	searches   []string
	seasonIDs  []string
	episodeIDs []string
	seasonsErr error
}

func (s *showProvider) Search(q string) ([]media.SearchResult, error) {
	s.searches = append(s.searches, q)
	return s.results, nil
}

func (s *showProvider) GetSeasons(id string) ([]media.Season, error) {
	s.seasonIDs = append(s.seasonIDs, id)
	return []media.Season{{ID: "s1", Number: 1}}, s.seasonsErr
}

func (s *showProvider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	s.episodeIDs = append(s.episodeIDs, id)
	return []media.Episode{{ID: "e1", Number: 1}}, nil
}

func (s *showProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	return nil, errors.New("unused")
}

func TestSeasonsAndEpisodesUseTheProvidersOwnIDForACatalogueRow(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/one-piece-live-action-9", Title: "One Piece (Live Action)", Type: media.TV, Year: "2023"},
		{ID: "movie/one-piece-film-1", Title: "One Piece", Type: media.Movie, Year: "1999"},
		{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"},
	}}
	p := Wrap(inner, okServer(t), Anime)
	rows, err := p.Trending(media.TV)
	if err != nil || len(rows) != 1 || rows[0].ID != "tv/37854" {
		t.Fatalf("Trending = %v, %v", rows, err)
	}

	if _, err := p.GetSeasons("tv/37854"); err != nil {
		t.Fatalf("GetSeasons: %v", err)
	}
	if _, err := p.GetEpisodes("tv/37854", "s1"); err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/one-piece-100" {
		t.Fatalf("GetSeasons got %v, want tv/one-piece-100", inner.seasonIDs)
	}
	if len(inner.episodeIDs) != 1 || inner.episodeIDs[0] != "tv/one-piece-100" {
		t.Fatalf("GetEpisodes got %v", inner.episodeIDs)
	}
	if len(inner.searches) != 1 {
		t.Fatalf("searches = %v, want the resolution cached", inner.searches)
	}
}

func TestSeasonsFailWhenTheProviderHasNoMatchingShow(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/other-1", Title: "Other Show", Type: media.TV}}}
	p := Wrap(inner, okServer(t), Anime)
	if _, err := p.Trending(media.TV); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSeasons("tv/37854"); err == nil {
		t.Fatal("GetSeasons succeeded with no matching show")
	}
	if len(inner.seasonIDs) != 0 {
		t.Fatalf("provider was called with %v", inner.seasonIDs)
	}
}

func TestSeasonsPassThroughForNativeIDs(t *testing.T) {
	inner := &showProvider{}
	p := Wrap(inner, okServer(t), General)
	if _, err := p.GetSeasons("tv/native-5"); err != nil {
		t.Fatal(err)
	}
	if len(inner.searches) != 0 || inner.seasonIDs[0] != "tv/native-5" {
		t.Fatalf("native id was rewritten: searches=%v ids=%v", inner.searches, inner.seasonIDs)
	}
}

func utopiaServer(t *testing.T) *Client {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[{"id":1,"name":"Utopia","first_air_date":"2013-01-15"}]}`)
	})
	return c
}

func TestSeasonsRejectASameTitleShowFromAnotherYear(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, utopiaServer(t), General)
	if _, err := p.Trending(media.TV); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSeasons("tv/1"); err == nil {
		t.Fatal("GetSeasons resolved Utopia 2013 to the 2020 series")
	}
	if len(inner.seasonIDs) != 0 {
		t.Fatalf("provider was called with %v", inner.seasonIDs)
	}
}

func TestSeasonsKeepAYearlessSameTitleCandidate(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-x", Title: "Utopia", Type: media.TV},
	}}
	p := Wrap(inner, utopiaServer(t), General)
	if _, err := p.Trending(media.TV); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatal(err)
	}
	if inner.seasonIDs[0] != "tv/utopia-x" {
		t.Fatalf("GetSeasons got %v, want tv/utopia-x", inner.seasonIDs)
	}
}

func TestResolveRowKeepsTheCatalogueIDAndNamesTheProvidersOwn(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"},
		{ID: "movie/the-matrix-7", Title: "The Matrix", Type: media.Movie, Year: "1999"},
	}}
	p := Wrap(inner, okServer(t), General)
	for _, tc := range []struct{ row, want media.SearchResult }{
		{
			media.SearchResult{ID: "tv/37854", Title: "One Piece", Type: media.TV, Year: "1999", FromCatalogue: true},
			media.SearchResult{ID: "tv/37854", Title: "One Piece", Type: media.TV, Year: "1999", FromCatalogue: true, ProviderID: "tv/one-piece-100"},
		},
		{
			media.SearchResult{ID: "movie/603", Title: "The Matrix", Type: media.Movie, Year: "1999", FromCatalogue: true},
			media.SearchResult{ID: "movie/603", Title: "The Matrix", Type: media.Movie, Year: "1999", FromCatalogue: true, ProviderID: "movie/the-matrix-7"},
		},
	} {
		if got := p.ResolveRow(tc.row); got != tc.want {
			t.Fatalf("ResolveRow(%+v) = %+v, want %+v", tc.row, got, tc.want)
		}
	}
}

func TestResolveRowMarksACatalogueRowTheProviderDoesNotHave(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/x-1", Title: "X", Type: media.TV, Year: "2020"}}}
	p := Wrap(inner, okServer(t), General)
	row := media.SearchResult{ID: "tv/404", Title: "Missing", Type: media.TV, Year: "2020", FromCatalogue: true}
	got := p.ResolveRow(row)
	want := row
	want.Unresolved = true
	if got != want || len(inner.searches) != 1 {
		t.Fatalf("ResolveRow = %+v after %d searches, want %+v after 1", got, len(inner.searches), want)
	}
}

func TestResolveRowLeavesNativeRowsAlone(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/x-1", Title: "X", Type: media.TV}}}
	p := Wrap(inner, okServer(t), General)
	for _, row := range []media.SearchResult{
		{ID: "tv/native-5", Title: "X", Type: media.TV},
		{ID: "movie/free-the-exorcist-hd-75043", Title: "X", Type: media.Movie},
		{ID: "movie/550", Title: "X", Type: media.Movie},
		{ID: "tv/37854", Title: "X", Type: media.TV},
	} {
		if got := p.ResolveRow(row); got != row {
			t.Fatalf("ResolveRow(%+v) = %+v", row, got)
		}
	}
}

func TestResolveRowRetryOfAFailedRowClearsUnresolved(t *testing.T) {
	inner := &showProvider{}
	p := Wrap(inner, okServer(t), General)
	failed := p.ResolveRow(media.SearchResult{ID: "tv/37854", Title: "One Piece", Type: media.TV, Year: "1999", FromCatalogue: true})
	if !failed.Unresolved {
		t.Fatalf("first resolve = %+v, want Unresolved", failed)
	}
	inner.results = []media.SearchResult{{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"}}
	got := p.ResolveRow(failed)
	if got.Unresolved || got.ProviderID != "tv/one-piece-100" || got.ID != "tv/37854" {
		t.Fatalf("retry = %+v, want resolved with the catalogue ID kept", got)
	}
}

type detailProvider struct {
	showProvider
	detailIDs []string
}

func (d *detailProvider) GetDetails(id string) (*media.ContentDetail, error) {
	d.detailIDs = append(d.detailIDs, id)
	return &media.ContentDetail{Description: "from provider"}, nil
}

func TestDetailsFallbackAsksTheProviderForItsOwnID(t *testing.T) {
	inner := &detailProvider{showProvider: showProvider{results: []media.SearchResult{
		{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"},
	}}}
	p := Wrap(inner, failingClient(t, 500), General)
	p.remember([]media.SearchResult{{ID: "tv/37854", Title: "One Piece", Type: media.TV, Year: "1999"}})
	d, err := p.GetDetails("tv/37854")
	if err != nil || d.Description != "from provider" {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if len(inner.detailIDs) != 1 || inner.detailIDs[0] != "tv/one-piece-100" {
		t.Fatalf("provider asked for %v, want tv/one-piece-100", inner.detailIDs)
	}
}

func TestDetailsFallbackNeverSendsAnUnresolvedCatalogueID(t *testing.T) {
	inner := &detailProvider{showProvider: showProvider{}}
	p := Wrap(inner, failingClient(t, 500), General)
	p.remember([]media.SearchResult{{ID: "tv/37854", Title: "One Piece", Type: media.TV}})
	if _, err := p.GetDetails("tv/37854"); err == nil {
		t.Fatal("GetDetails succeeded for a show the provider does not have")
	}
	if len(inner.detailIDs) != 0 {
		t.Fatalf("provider was asked for %v", inner.detailIDs)
	}
}

func TestDetailsFailureDoesNotQueueAListNotice(t *testing.T) {
	inner := &detailProvider{}
	p := Wrap(inner, failingClient(t, 401), General)
	if _, err := p.GetDetails("movie/603"); err != nil {
		t.Fatal(err)
	}
	if n := p.TakeNotice(); n != "" {
		t.Fatalf("notice = %q, want none from a metadata failure", n)
	}
}

func TestResolveRowWithoutAYearRefusesTwoSameTitleCandidates(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved when two works share the title", got)
	}
}

func TestTrendingWithNoticeReturnsTheNoticeAndLeavesNoPendingOne(t *testing.T) {
	inner := &fakeInner{trending: providerRow}
	p := Wrap(inner, failingClient(t, 401), General)
	got, notice, err := p.TrendingWithNotice(media.Movie)
	if err != nil || len(got) != 1 || !strings.Contains(notice, "tmdb_api_key rejected") {
		t.Fatalf("TrendingWithNotice = %v, %q, %v", got, notice, err)
	}
	if n := p.TakeNotice(); n != "" {
		t.Fatalf("pending notice = %q, want none: another fetch could have taken it", n)
	}
}

func TestResolveRowWithAYearRefusesTwoYearlessSameTitleCandidates(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-a", Title: "Utopia", Type: media.TV},
		{ID: "tv/utopia-b", Title: "Utopia", Type: media.TV},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved for two yearless candidates", got)
	}
}

func TestResolveRowWithAYearIgnoresWrongYearsAmongYearlessCandidates(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
		{ID: "tv/utopia-x", Title: "Utopia", Type: media.TV},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if got.Unresolved || got.ProviderID != "tv/utopia-x" {
		t.Fatalf("ResolveRow = %+v, want the single yearless candidate", got)
	}
}

func TestResolveRowWithAYearPrefersTheExactYearOverYearlessCandidates(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-a", Title: "Utopia", Type: media.TV},
		{ID: "tv/utopia-b", Title: "Utopia", Type: media.TV},
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if got.Unresolved || got.ProviderID != "tv/utopia-2020" {
		t.Fatalf("ResolveRow = %+v, want tv/utopia-2020", got)
	}
}

func TestResolveRowTreatsDuplicateIDsAsOneCandidate(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-a", Title: "Utopia", Type: media.TV},
		{ID: "tv/utopia-a", Title: "Utopia", Type: media.TV},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if got.Unresolved || got.ProviderID != "tv/utopia-a" {
		t.Fatalf("ResolveRow = %+v, want tv/utopia-a", got)
	}
}

func TestResolveRowWithNoCandidatesIsUnresolved(t *testing.T) {
	p := Wrap(&showProvider{}, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if !got.Unresolved {
		t.Fatalf("ResolveRow = %+v, want Unresolved", got)
	}
}

func TestResolveRowDoesNotReuseACachedMatchForTheSameIDWithAnotherYear(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	p := Wrap(inner, okServer(t), General)
	first := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2013", FromCatalogue: true})
	if first.ProviderID != "tv/utopia-2013" {
		t.Fatalf("first ResolveRow = %+v", first)
	}
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved: the cached 2013 match is not the 2020 row", got)
	}
}

func TestResolveRowDoesNotReuseACachedMatchForTheSameIDWithAnotherTitle(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, okServer(t), General)
	first := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if first.ProviderID != "tv/utopia" {
		t.Fatalf("first ResolveRow = %+v", first)
	}
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Dystopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved for a different title", got)
	}
}

func TestResolveRowReusesACachedMatchForTheSameRow(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, okServer(t), General)
	row := media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true}
	p.ResolveRow(row)
	p.ResolveRow(row)
	if len(inner.searches) != 1 {
		t.Fatalf("searches = %d, want 1", len(inner.searches))
	}
}

func TestResolveRowRefusesTwoDistinctWorksWithTheExactYear(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-a", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-b", Title: "Utopia", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved when two works share title and year", got)
	}
}

type tmdbIDProvider struct {
	*fakeInner
	searches int
}

func (*tmdbIDProvider) MintsTMDBShapedIDs() bool { return true }

func (p *tmdbIDProvider) Search(string) ([]media.SearchResult, error) {
	p.searches++
	return nil, errors.New("search down")
}

func TestResolveRowKeepsTheDirectTMDBIDForAProviderWithTMDBShapedIDs(t *testing.T) {
	inner := &tmdbIDProvider{fakeInner: &fakeInner{}}
	p := Wrap(inner, nil, General)
	row := media.SearchResult{ID: "tv/37854", Title: "One Piece", Type: media.TV, FromCatalogue: true}

	got := p.ResolveRow(row)
	if got.Unresolved || got.ProviderID != "tv/37854" {
		t.Fatalf("ResolveRow = %+v, want the direct TMDB ID and not Unresolved", got)
	}
	if inner.searches != 0 {
		t.Fatalf("provider searched %d times; a provider with TMDB-shaped IDs needs no title match", inner.searches)
	}
	if !MintsTMDBShapedIDs(inner) || MintsTMDBShapedIDs(&showProvider{}) {
		t.Fatal("MintsTMDBShapedIDs must be true only for a provider that declares it")
	}
}

func TestResolveRowRefusesATitleWithNothingToCompare(t *testing.T) {
	for _, title := range []string{"...", "", "   ", "'’"} {
		t.Run(title, func(t *testing.T) {
			inner := &showProvider{results: []media.SearchResult{
				{ID: "tv/unrelated-1", Title: "!!!", Type: media.TV, Year: "2020"},
			}}
			p := Wrap(inner, okServer(t), General)
			got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: title, Type: media.TV, Year: "2020", FromCatalogue: true})
			if !got.Unresolved || got.ProviderID != "" {
				t.Fatalf("ResolveRow = %+v, want Unresolved with no provider ID", got)
			}
			if len(inner.searches) != 0 {
				t.Fatalf("provider searched %q for a title with nothing to compare", inner.searches)
			}
		})
	}
}

func TestSeasonsFailForACatalogueRowWhoseTitleHasNothingToCompare(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"results":[{"id":7,"name":"...","first_air_date":"2020-01-01"}]}`)
	})
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/unrelated-1", Title: "!!!", Type: media.TV, Year: "2020"},
	}}
	p := Wrap(inner, c, General)
	rows, err := p.Trending(media.TV)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Trending = %+v, %v", rows, err)
	}
	if _, err := p.GetSeasons(rows[0].ID); err == nil {
		t.Fatalf("GetSeasons asked the provider for %q; want an error", inner.seasonIDs)
	}
	if len(inner.seasonIDs) != 0 {
		t.Fatalf("provider was asked for seasons of %q", inner.seasonIDs)
	}
}

func TestResolveRowKeepsTheDirectTMDBIDWhenTheTitleHasNothingToCompare(t *testing.T) {
	inner := &tmdbIDProvider{fakeInner: &fakeInner{}}
	p := Wrap(inner, nil, General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/37854", Title: "...", Type: media.TV, FromCatalogue: true})
	if got.Unresolved || got.ProviderID != "tv/37854" {
		t.Fatalf("ResolveRow = %+v, want the direct TMDB ID and not Unresolved", got)
	}
}

func TestResolveRowWithoutAYearNeverTakesTheOnlySameTitledWork(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"}}}
	p := Wrap(inner, okServer(t), General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true})
	if !got.Unresolved || got.ProviderID != "" {
		t.Fatalf("ResolveRow = %+v, want Unresolved: a title alone cannot tell Utopia from its remake", got)
	}
}

func TestSeasonsAndEpisodesOfAYearlessCatalogueRowUseTheProvidersWorkFromTheCatalogueYear(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p := Wrap(inner, c, General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}})
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatalf("GetSeasons: %v", err)
	}
	if _, err := p.GetEpisodes("tv/1", "s1"); err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/utopia-2013" || len(inner.episodeIDs) != 1 || inner.episodeIDs[0] != "tv/utopia-2013" {
		t.Fatalf("seasons asked for %v, episodes for %v; want tv/utopia-2013 for both", inner.seasonIDs, inner.episodeIDs)
	}
}

func TestEpisodesOfAYearlessCatalogueRowKeepTheYearTheSeasonsLookupFound(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"}}}
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p := Wrap(inner, c, General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}})
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatalf("GetSeasons: %v", err)
	}
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Second) }
	if _, err := p.GetEpisodes("tv/1", "s1"); err != nil {
		t.Fatalf("GetEpisodes after the details expired and TMDB failed: %v", err)
	}
	if len(inner.episodeIDs) != 1 || inner.episodeIDs[0] != "tv/utopia-2013" {
		t.Fatalf("episodes asked for %v; want tv/utopia-2013", inner.episodeIDs)
	}
}

func TestSeasonsOfAYearlessCatalogueRowNeverReachTheProviderOnATitleMatch(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"}}}
	p := Wrap(inner, okServer(t), General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}})
	_, _ = p.GetSeasons("tv/1")
	for _, id := range inner.seasonIDs {
		if id == "tv/utopia-2020" {
			t.Fatalf("provider was asked for the seasons of %s, matched on title alone", id)
		}
	}
}

func TestResolveRowOfAYearlessRowCarriesTheCatalogueYearItMatchedOn(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p := Wrap(inner, c, General)
	row := media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}
	for i := range 2 {
		got := p.ResolveRow(row)
		if got.Unresolved || got.ProviderID != "tv/utopia-2013" || got.Year != "2013" {
			t.Fatalf("ResolveRow call %d = %+v, want tv/utopia-2013 with Year 2013", i+1, got)
		}
	}
	if len(inner.searches) != 1 {
		t.Fatalf("searches = %d, want 1: the second call is a cache hit", len(inner.searches))
	}
}

func TestResolveRowKeepsTheYearARowAlreadyHas(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{{ID: "tv/utopia", Title: "Utopia", Type: media.TV}}}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p := Wrap(inner, c, General)
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true})
	if got.ProviderID != "tv/utopia" || got.Year != "2020" {
		t.Fatalf("ResolveRow = %+v, want tv/utopia with its own Year 2020", got)
	}
}

func TestAYearlessListingRowKeepsTheYearAlreadyKnownForItsID(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	p := Wrap(inner, failingClient(t, 500), General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2013", FromCatalogue: true}})
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}})
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatalf("GetSeasons after a yearless refresh with TMDB down: %v", err)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/utopia-2013" {
		t.Fatalf("seasons asked for %v; want tv/utopia-2013", inner.seasonIDs)
	}
}

func TestADatedListingRowReplacesTheYearKnownForItsID(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	p := Wrap(inner, failingClient(t, 500), General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2013", FromCatalogue: true}})
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2020", FromCatalogue: true}})
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatal(err)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/utopia-2020" {
		t.Fatalf("seasons asked for %v; want tv/utopia-2020", inner.seasonIDs)
	}
}

func TestResolveRowOfAYearlessRowUsesTheYearAlreadyKnownForItsID(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	p := Wrap(inner, failingClient(t, 500), General)
	p.remember([]media.SearchResult{{ID: "tv/1", Title: "Utopia", Type: media.TV, Year: "2013", FromCatalogue: true}})
	got := p.ResolveRow(media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true})
	if got.Unresolved || got.ProviderID != "tv/utopia-2013" || got.Year != "2013" {
		t.Fatalf("ResolveRow with TMDB down = %+v, want tv/utopia-2013 from the year already known", got)
	}
}

func TestSeasonsOfARowResolveRowDatedUseTheCachedMatchWhenTMDBLaterFails(t *testing.T) {
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls > 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p := Wrap(inner, c, General)
	row := media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}
	p.remember([]media.SearchResult{row})
	if got := p.ResolveRow(row); got.ProviderID != "tv/utopia-2013" {
		t.Fatalf("ResolveRow = %+v, want tv/utopia-2013", got)
	}
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Second) }
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatalf("GetSeasons after the details expired and TMDB failed: %v", err)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/utopia-2013" {
		t.Fatalf("seasons asked for %v; want tv/utopia-2013", inner.seasonIDs)
	}
	if len(inner.searches) != 1 {
		t.Fatalf("searches = %d, want 1: seasons reuse the cached match", len(inner.searches))
	}
}

// yearHydrationRace has TMDB's details answer 2013 while a listing that dates
// the same ID 2020 is remembered mid-request.
func yearHydrationRace(t *testing.T) (*Provider, *showProvider, media.SearchResult) {
	t.Helper()
	inner := &showProvider{results: []media.SearchResult{
		{ID: "tv/utopia-2020", Title: "Utopia", Type: media.TV, Year: "2020"},
		{ID: "tv/utopia-2013", Title: "Utopia", Type: media.TV, Year: "2013"},
	}}
	var p *Provider
	row := media.SearchResult{ID: "tv/1", Title: "Utopia", Type: media.TV, FromCatalogue: true}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		dated := row
		dated.Year = "2020"
		p.remember([]media.SearchResult{dated})
		io.WriteString(w, `{"id":1,"first_air_date":"2013-01-15"}`)
	})
	p = Wrap(inner, c, General)
	p.remember([]media.SearchResult{row})
	return p, inner, row
}

func TestSeasonsYearHydrationKeepsANewerYearRememberedMeanwhile(t *testing.T) {
	p, inner, _ := yearHydrationRace(t)
	if _, err := p.GetSeasons("tv/1"); err != nil {
		t.Fatalf("GetSeasons: %v", err)
	}
	if _, err := p.GetEpisodes("tv/1", "s1"); err != nil {
		t.Fatalf("GetEpisodes: %v", err)
	}
	p.mu.Lock()
	got := p.rows["tv/1"].Year
	p.mu.Unlock()
	if got != "2020" {
		t.Fatalf("stored year = %q, want 2020 from the listing remembered during hydration", got)
	}
	if len(inner.seasonIDs) != 1 || inner.seasonIDs[0] != "tv/utopia-2020" || len(inner.episodeIDs) != 1 || inner.episodeIDs[0] != "tv/utopia-2020" {
		t.Fatalf("seasons asked for %v, episodes for %v; want tv/utopia-2020 for both", inner.seasonIDs, inner.episodeIDs)
	}
}

func TestResolveRowYearHydrationKeepsANewerYearRememberedMeanwhile(t *testing.T) {
	p, _, row := yearHydrationRace(t)
	resolved := p.ResolveRow(row)
	p.mu.Lock()
	got := p.rows["tv/1"].Year
	p.mu.Unlock()
	if got != "2020" {
		t.Fatalf("stored year = %q, want 2020 from the listing remembered during hydration", got)
	}
	if resolved.ProviderID != "tv/utopia-2020" || resolved.Year != "2020" {
		t.Fatalf("ResolveRow = %+v, want tv/utopia-2020 from the year now stored", resolved)
	}
}
