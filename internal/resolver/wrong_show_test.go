package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

type watchCountingSP struct {
	*fakeSP
	watched int
}

func (w *watchCountingSP) Watch(string, string, string, string) (*media.Stream, error) {
	w.watched++
	return w.stream, w.watchErr
}

type AnimeCatalogSP struct{ *watchCountingSP }

type SeriesCatalogSP struct{ *watchCountingSP }

func derryRequest() Request {
	return Request{
		ID:        "series/it-welcome-to-derry-65664",
		Title:     "IT: Welcome to Derry",
		Year:      "2025",
		MediaType: media.TV,
		Season:    1,
		Episode:   1,
	}
}

func TestResolveWithProviderRefusesAnUnrelatedTitle(t *testing.T) {
	outcast := media.SearchResult{ID: "DzDK9RczVWzlOMrw", Title: "Welcome to the Outcast's Restaurant!", Type: media.TV, Year: "2025"}
	for _, tc := range []struct {
		name string
		req  Request
		rows []media.SearchResult
	}{
		{"real AnimeOnsen row for the Derry ref", derryRequest(), []media.SearchResult{outcast}},
		{"shared leading words only", Request{Title: "Welcome to Derry", MediaType: media.TV, Season: 1, Episode: 1}, []media.SearchResult{outcast}},
		{"colon prefix alone", derryRequest(), []media.SearchResult{{ID: "x/1", Title: "IT", Type: media.TV, Year: "2025"}}},
		{"colon prefix dropped", derryRequest(), []media.SearchResult{{ID: "x/2", Title: "Welcome to Derry", Type: media.TV, Year: "2025"}}},
		{"sibling title, same year", Request{Title: "Star Trek", Year: "2025", MediaType: media.TV, Season: 1, Episode: 1}, []media.SearchResult{{ID: "x/3", Title: "Star Trek: Discovery", Type: media.TV, Year: "2025"}}},
		{"empty request title", Request{Title: "", MediaType: media.TV, Season: 1, Episode: 1}, []media.SearchResult{outcast}},
		{"empty candidate title", derryRequest(), []media.SearchResult{{ID: "x/4", Title: "", Type: media.TV}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &watchCountingSP{fakeSP: &fakeSP{searchResults: tc.rows, stream: &media.Stream{URL: "https://cdn/wrong.mpd"}}}
			got, stage, err := resolveWithProvider(p, tc.req, func(string, ...any) {})
			if got != nil || err == nil {
				t.Fatalf("served %v for %q from %q; want a refusal", got, tc.req.Title, tc.rows[0].Title)
			}
			if stage != "match" || !strings.Contains(err.Error(), "no matching result") {
				t.Fatalf("stage %q err %v, want a match-stage refusal", stage, err)
			}
			if p.watched != 0 {
				t.Fatalf("Watch called %d times for a title that is not the request", p.watched)
			}
		})
	}
}

func TestResolveWithProviderStillServesTheRequestedTitle(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  media.SearchResult
	}{
		{"same title, colon prefix kept", media.SearchResult{ID: "series/other-1", Title: "IT: Welcome to Derry", Type: media.TV, Year: "2025"}},
		{"year one apart", media.SearchResult{ID: "series/other-2", Title: "IT: Welcome to Derry", Type: media.TV, Year: "2026"}},
		{"year unknown", media.SearchResult{ID: "series/other-3", Title: "IT: Welcome to Derry", Type: media.TV}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &watchCountingSP{fakeSP: &fakeSP{searchResults: []media.SearchResult{tc.row}, stream: &media.Stream{URL: "https://cdn/ok.m3u8"}}}
			got, _, err := resolveWithProvider(p, derryRequest(), func(string, ...any) {})
			if err != nil || got == nil {
				t.Fatalf("got %v / %v, want the stream", got, err)
			}
		})
	}
}

func TestResolveReachesTheRightSourceAfterAWrongShowRow(t *testing.T) {
	anime := &AnimeCatalogSP{&watchCountingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: "DzDK9RczVWzlOMrw", Title: "Welcome to the Outcast's Restaurant!", Type: media.TV, Year: "2025"}},
		stream:        &media.Stream{URL: "https://cdn.animeonsen.xyz/video/mp4-dash/DzDK9RczVWzlOMrw/1/manifest.mpd"},
	}}}
	series := &SeriesCatalogSP{&watchCountingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: "series/it-welcome-to-derry-65664", Title: "IT: Welcome to Derry", Type: media.TV, Year: "2025"}},
		stream:        &media.Stream{URL: "https://cdn/derry.m3u8"},
	}}}
	r := New([]provider.Provider{anime, series}, NewHealthStore(), func(string, ...any) {})
	r.validate = false
	r.batchSize = 1

	got, rep, err := r.Resolve(context.Background(), derryRequest())
	if err != nil || got == nil {
		t.Fatalf("got %v / %v; the right show was never reached: %s", got, err, rep.Summary())
	}
	if got.URL != "https://cdn/derry.m3u8" {
		t.Fatalf("served %s, want the Derry stream", got.URL)
	}
	if anime.watched != 0 {
		t.Fatalf("the unrelated catalogue was asked for a stream %d times", anime.watched)
	}
	if len(rep.Attempts) != 2 || rep.Attempts[0].Err == nil {
		t.Fatalf("the wrong-show source must be a recorded failure, got %s", rep.Summary())
	}
}

func TestResolveWithProviderLogsASearchFailure(t *testing.T) {
	p := &watchCountingSP{fakeSP: &fakeSP{searchErr: errors.New("response was not a recognisable search page")}}
	var logged []string
	log := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	_, stage, err := resolveWithProvider(p, derryRequest(), log)
	if err == nil || stage != "search" {
		t.Fatalf("stage %q err %v, want a search failure", stage, err)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "not a recognisable search page") {
		t.Fatalf("search failure missing from the debug log: %q", logged)
	}
}

func TestResolveWithProviderLogsAnUnmatchedSearch(t *testing.T) {
	p := &watchCountingSP{fakeSP: &fakeSP{searchResults: []media.SearchResult{{ID: "a/1", Title: "Something Else", Type: media.TV}}}}
	var logged []string
	log := func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }
	_, _, _ = resolveWithProvider(p, derryRequest(), log)
	if !strings.Contains(strings.Join(logged, "\n"), "refused") {
		t.Fatalf("refused candidate missing from the debug log: %q", logged)
	}
}

func TestResolveWithProviderAdmitsTheExactTitleBeyondTheTopFiveUnrelatedRows(t *testing.T) {
	var rows []media.SearchResult
	for i := 0; i < 6; i++ {
		rows = append(rows, media.SearchResult{ID: fmt.Sprintf("x/%d", i), Title: fmt.Sprintf("Lost Boys %d", i), Type: media.TV, Year: "2025"})
	}
	rows = append(rows, media.SearchResult{ID: "x/exact", Title: "Lost", Type: media.TV})
	p := &watchCountingSP{fakeSP: &fakeSP{searchResults: rows, stream: &media.Stream{URL: "https://cdn/lost.m3u8"}}}
	got, _, err := resolveWithProvider(p, Request{Title: "Lost", Year: "2025", MediaType: media.TV, Season: 1, Episode: 1}, func(string, ...any) {})
	if err != nil || got == nil {
		t.Fatalf("got %v / %v, want the exact-title row to survive the cap", got, err)
	}
}

func TestResolveWithProviderAdmitsANyaaSequelRow(t *testing.T) {
	p := &watchCountingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: "nyaa/One-Punch+Man?s=2", Title: "One Punch Man Season 2", Type: media.TV}},
		stream:        &media.Stream{URL: "magnet:?xt=urn:btih:abc"},
	}}
	got, _, err := resolveWithProvider(p, Request{Title: "One Punch Man 2nd Season", MediaType: media.TV, Season: 1, Episode: 1}, func(string, ...any) {})
	if err != nil || got == nil {
		t.Fatalf("got %v / %v, want the sequel row admitted", got, err)
	}
}

func TestResolveWithProviderKeepsTheCrossTypeFallback(t *testing.T) {
	p := &watchCountingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{{ID: "cartoon/foo", Title: "Foo", Type: media.TV}},
		stream:        &media.Stream{URL: "https://cdn/foo.m3u8"},
	}}
	got, _, err := resolveWithProvider(p, Request{Title: "Foo", MediaType: media.Movie}, func(string, ...any) {})
	if err != nil || got == nil {
		t.Fatalf("got %v / %v, want a same-title row of the other media type still tried", got, err)
	}
}
