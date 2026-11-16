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
			io.WriteString(w, `{"overview":"A hacker.","external_ids":{"imdb_id":"tt0133093"}}`)
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
	inner := &fakeInner{err: errors.New("provider down")}
	p := Wrap(inner, failingClient(t, 429), General)
	got, err := p.Trending(media.Movie)
	if err == nil || len(got) != 0 {
		t.Fatalf("Trending = %v, %v; want the provider's error", got, err)
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
