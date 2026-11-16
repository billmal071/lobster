package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"lobster/internal/catalog"
	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

type listProvider struct {
	provider.Provider
	trending, recent int
}

func (l *listProvider) Trending(media.MediaType) ([]media.SearchResult, error) {
	l.trending++
	return []media.SearchResult{{ID: "movie/slug-1", Title: "From Provider"}}, nil
}

func (l *listProvider) Recent(media.MediaType) ([]media.SearchResult, error) {
	l.recent++
	return []media.SearchResult{{ID: "movie/slug-2", Title: "Recent From Provider"}}, nil
}

func catalogueFixture(t *testing.T, key string, h http.HandlerFunc) (warnings *[]string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	prevCfg, prevWarn, prevNew := cfg, warnf, newCatalogClient
	t.Cleanup(func() { cfg, warnf, newCatalogClient = prevCfg, prevWarn, prevNew })
	cfg = config.Default()
	cfg.TMDBAPIKey = key
	newCatalogClient = func(k string) *catalog.Client { return catalog.NewWithBase(k, srv.URL, srv.Client()) }

	var got []string
	warnf = func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }
	return &got
}

func TestTrendingUsesCatalogueWhenKeySet(t *testing.T) {
	w := catalogueFixture(t, "abc", func(rw http.ResponseWriter, r *http.Request) {
		io.WriteString(rw, `{"results":[{"id":603,"title":"The Matrix","release_date":"1999-03-30"}]}`)
	})
	p := &listProvider{}
	got, err := listResults(p, media.Movie, false)
	if err != nil || len(got) != 1 || got[0].ID != "movie/603" {
		t.Fatalf("got %v, %v", got, err)
	}
	if p.trending != 0 {
		t.Error("provider consulted")
	}
	if len(*w) != 0 {
		t.Errorf("unexpected warnings %v", *w)
	}
}

func TestRecentUsesCatalogueWhenKeySet(t *testing.T) {
	var path string
	catalogueFixture(t, "abc", func(rw http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		io.WriteString(rw, `{"results":[{"id":5,"name":"Show"}]}`)
	})
	p := &listProvider{}
	got, err := listResults(p, media.TV, true)
	if err != nil || len(got) != 1 || got[0].ID != "tv/5" || path != "/tv/on_the_air" {
		t.Fatalf("got %v, %v, path %s", got, err, path)
	}
	if p.recent != 0 {
		t.Error("provider consulted")
	}
}

func TestNoKeyUsesOnlyTheProviderAndHintsOnce(t *testing.T) {
	w := catalogueFixture(t, "", func(rw http.ResponseWriter, r *http.Request) {
		t.Error("catalogue contacted without a key")
	})
	p := &listProvider{}
	got, err := listResults(p, media.Movie, false)
	if err != nil || len(got) != 1 || got[0].Title != "From Provider" || p.trending != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(*w) != 1 || !strings.Contains((*w)[0], "tmdb_api_key") {
		t.Errorf("warnings = %v, want one hint naming tmdb_api_key", *w)
	}
}

func TestRejectedKeyFallsBackWithOneLine(t *testing.T) {
	w := catalogueFixture(t, "bad", func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(http.StatusUnauthorized) })
	p := &listProvider{}
	got, err := listResults(p, media.Movie, false)
	if err != nil || len(got) != 1 || got[0].Title != "From Provider" {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(*w) != 1 || !strings.Contains((*w)[0], "tmdb_api_key rejected") {
		t.Errorf("warnings = %v", *w)
	}
}

func TestRateLimitFallsBackWithOneLine(t *testing.T) {
	w := catalogueFixture(t, "abc", func(rw http.ResponseWriter, r *http.Request) { rw.WriteHeader(http.StatusTooManyRequests) })
	p := &listProvider{}
	got, err := listResults(p, media.Movie, true)
	if err != nil || len(got) != 1 || got[0].Title != "Recent From Provider" {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(*w) != 1 || !strings.Contains((*w)[0], "rate limit") {
		t.Errorf("warnings = %v", *w)
	}
}

type tvSearchProvider struct {
	listProvider
}

func (tvSearchProvider) Search(string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "tv/one-piece-100", Title: "One Piece", Type: media.TV, Year: "1999"}}, nil
}

func TestTrendingAndRecentPlayCatalogueTVRowsUnderTheProvidersID(t *testing.T) {
	for name, run := range map[string]func(*cobra.Command, []string) error{"trending": trendingRun, "recent": recentRun} {
		t.Run(name, func(t *testing.T) {
			catalogueFixture(t, "abc", func(rw http.ResponseWriter, r *http.Request) {
				io.WriteString(rw, `{"results":[{"id":37854,"name":"One Piece","first_air_date":"1999-10-20"}]}`)
			})
			prevProv, prevSel, prevPlay := agentProvider, selectItem, playSelected
			t.Cleanup(func() { agentProvider, selectItem, playSelected = prevProv, prevSel, prevPlay })
			agentProvider = func() provider.Provider { return &tvSearchProvider{} }
			selectItem = func(string, []string) (int, error) { return 0, nil }
			var played media.SearchResult
			playSelected = func(_ provider.Provider, sel media.SearchResult, _, _ int) error {
				played = sel
				return nil
			}
			if err := run(nil, []string{"tv"}); err != nil {
				t.Fatal(err)
			}
			if played.ID != "tv/one-piece-100" {
				t.Fatalf("played %+v, want the provider's id", played)
			}
		})
	}
}
