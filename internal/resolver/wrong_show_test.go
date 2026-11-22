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

func TestResolveWithProviderKeepsTheCrossTypeMatchBesideAnUnrelatedSameTypeRow(t *testing.T) {
	p := &watchCountingSP{fakeSP: &fakeSP{
		searchResults: []media.SearchResult{
			{ID: "movie/bar", Title: "Bar", Type: media.Movie},
			{ID: "cartoon/foo", Title: "Foo", Type: media.TV},
		},
		stream: &media.Stream{URL: "https://cdn/foo.m3u8"},
	}}
	got, _, err := resolveWithProvider(p, Request{Title: "Foo", MediaType: media.Movie}, func(string, ...any) {})
	if err != nil || got == nil {
		t.Fatalf("got %v / %v, want the matching other-type row tried after the unrelated row is refused", got, err)
	}
}

func admits(t *testing.T, req Request, row media.SearchResult) bool {
	t.Helper()
	p := &watchCountingSP{fakeSP: &fakeSP{searchResults: []media.SearchResult{row}, stream: &media.Stream{URL: "https://cdn/x.m3u8"}}}
	got, _, err := resolveWithProvider(p, req, func(string, ...any) {})
	return err == nil && got != nil
}

func TestAdmissionOfNyaaSequelRows(t *testing.T) {
	row := media.SearchResult{ID: "nyaa/One+Punch+Man?s=2", Title: "One Punch Man Season 2", Type: media.TV}
	other := media.SearchResult{ID: "nyaa/Mob+Psycho+100?s=2", Title: "Mob Psycho 100 Season 2", Type: media.TV}
	for _, tc := range []struct {
		name  string
		req   Request
		row   media.SearchResult
		admit bool
	}{
		{"plain title, matching season", Request{Title: "One Punch Man", MediaType: media.TV, Season: 2, Episode: 3}, row, true},
		{"plain title, other season", Request{Title: "One Punch Man", MediaType: media.TV, Season: 3, Episode: 3}, row, false},
		{"plain title, season one", Request{Title: "One Punch Man", MediaType: media.TV, Season: 1, Episode: 3}, row, false},
		{"plain title, different show's sequel row", Request{Title: "One Punch Man", MediaType: media.TV, Season: 2, Episode: 3}, other, false},
		{"season-marked title, different show's sequel row", Request{Title: "One Punch Man 2nd Season", MediaType: media.TV, Season: 1, Episode: 1}, other, false},
		{"season-marked title, its own sequel row", Request{Title: "One Punch Man 2nd Season", MediaType: media.TV, Season: 1, Episode: 1}, row, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := admits(t, tc.req, tc.row); got != tc.admit {
				t.Fatalf("admitted=%v, want %v", got, tc.admit)
			}
		})
	}
}

func TestAdmissionTreatsSeasonDesignatorsAsOneWork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   string
		row   string
		admit bool
	}{
		{"ordinal request, numbered row", "Mob Psycho 100 2nd Season", "Mob Psycho 100 Season 2", true},
		{"numbered request, ordinal row", "Mob Psycho 100 Season 2", "Mob Psycho 100 2nd Season", true},
		{"roman row", "Mob Psycho 100 2nd Season", "Mob Psycho 100 II", true},
		{"roman request", "Mob Psycho 100 III", "Mob Psycho 100 3rd Season", true},
		{"number word", "Mob Psycho 100 Season Two", "Mob Psycho 100 2nd Season", true},
		{"different season number", "Mob Psycho 100 2nd Season", "Mob Psycho 100 Season 3", false},
		{"season one request, season two row", "Mob Psycho 100", "Mob Psycho 100 Season 2", false},
		{"part is not a season", "Mob Psycho 100 Part 2", "Mob Psycho 100 Season 2", false},
		{"cour is not a season", "Mob Psycho 100 Cour 2", "Mob Psycho 100 2nd Season", false},
		{"different show, same season", "Mob Psycho 100 2nd Season", "One Punch Man Season 2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := media.SearchResult{ID: "opaque-1", Title: tc.row, Type: media.TV}
			if got := admits(t, Request{Title: tc.req, MediaType: media.TV, Season: 1, Episode: 1}, row); got != tc.admit {
				t.Fatalf("request %q row %q: admitted=%v, want %v", tc.req, tc.row, got, tc.admit)
			}
		})
	}
}

func TestAdmissionTrustsAnIdenticalIDOverTheTitle(t *testing.T) {
	req := Request{ID: "series/it-welcome-to-derry-65664", Title: "IT: Welcome to Derry", MediaType: media.TV, Season: 1, Episode: 1}
	row := media.SearchResult{ID: req.ID, Title: "Pennywise: The Series", Type: media.TV}
	if !admits(t, req, row) {
		t.Fatal("a row with the requested ID was refused")
	}
}

// Known limits: the gate refuses unrelated titles. It does not prove the row is the same work.
func TestAdmissionKnownLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		row  media.SearchResult
	}{
		{"part subtitle of a film", Request{Title: "Dune", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Dune: Part Two", Type: media.Movie}},
		{"later season of the same show", Request{Title: "Attack on Titan", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "t/1", Title: "Attack on Titan: Season 2", Type: media.TV}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !admits(t, tc.req, tc.row) {
				t.Fatalf("%q was refused for %q; update the documented limit if this is now stricter", tc.row.Title, tc.req.Title)
			}
		})
	}
}

func TestAdmissionRefusesANyaaSequelRowWhenNeitherSideHasATitleToCompare(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		row  media.SearchResult
	}{
		{"punctuation request, punctuation base", Request{Title: "...", MediaType: media.TV, Season: 2, Episode: 1}, media.SearchResult{ID: "nyaa/%21%21%21?s=2", Title: "Unrelated Season 2", Type: media.TV}},
		{"empty request, empty base", Request{Title: "", MediaType: media.TV, Season: 2, Episode: 1}, media.SearchResult{ID: "nyaa/?s=2", Title: "Unrelated Season 2", Type: media.TV}},
		{"bare season title, empty base", Request{Title: "Season 2", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "nyaa/?s=2", Title: "Unrelated", Type: media.TV}},
		{"punctuation before the season, punctuation base", Request{Title: "... Season 2", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "nyaa/---?s=2", Title: "Unrelated", Type: media.TV}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if admits(t, tc.req, tc.row) {
				t.Fatalf("%q (ID %s) was admitted for %q", tc.row.Title, tc.row.ID, tc.req.Title)
			}
		})
	}
}

func TestAdmissionRefusesASameTitledWorkFromAnotherYear(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		row  media.SearchResult
	}{
		{"later remake of a film", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2020"}},
		{"earlier original of a film", Request{Title: "Foo", Year: "2020", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2013"}},
		{"two years apart", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2015"}},
		{"remake of a series, accents folded", Request{Title: "Shogun", Year: "1980", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "t/2", Title: "Shōgun", Type: media.TV, Year: "2024"}},
		{"year qualifier in the row's title", Request{Title: "Doctor Who", Year: "1963", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "t/3", Title: "Doctor Who (2005)", Type: media.TV, Year: "2005"}},
		{"other media type", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "cartoon/foo", Title: "Foo", Type: media.TV, Year: "2020"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &watchCountingSP{fakeSP: &fakeSP{searchResults: []media.SearchResult{tc.row}, stream: &media.Stream{URL: "https://cdn/remake.m3u8"}}}
			got, stage, err := resolveWithProvider(p, tc.req, func(string, ...any) {})
			if got != nil || err == nil || stage != "match" {
				t.Fatalf("got %v stage=%q err=%v for the %s request; the only row is from %s", got, stage, err, tc.req.Year, tc.row.Year)
			}
			if p.watched != 0 {
				t.Fatalf("Watch called %d times for a work from %s", p.watched, tc.row.Year)
			}
		})
	}
}

func TestAdmissionKeepsARowWhoseYearIsUnknownOrWithinOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		row  media.SearchResult
	}{
		{"row one year later", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2014"}},
		{"row one year earlier", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2012"}},
		{"row without a year", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie}},
		{"request without a year", Request{Title: "Foo", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2020"}},
		{"row year that is not a number", Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "TBA"}},
		{"request year that is not a number", Request{Title: "Foo", Year: "2013-05-01", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2020"}},
		{"identical ID from another year", Request{ID: "m/1", Title: "Foo", Year: "2013", MediaType: media.Movie}, media.SearchResult{ID: "m/1", Title: "Foo", Type: media.Movie, Year: "2020"}},
		{"Nyaa sequel row from a later year", Request{Title: "One Punch Man", Year: "2015", MediaType: media.TV, Season: 2, Episode: 3}, media.SearchResult{ID: "nyaa/One+Punch+Man?s=2", Title: "One Punch Man Season 2", Type: media.TV, Year: "2019"}},
		{"season-marked request, its Nyaa row from a later year", Request{Title: "One Punch Man 2nd Season", Year: "2015", MediaType: media.TV, Season: 1, Episode: 1}, media.SearchResult{ID: "nyaa/One+Punch+Man?s=2", Title: "One Punch Man", Type: media.TV, Year: "2019"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !admits(t, tc.req, tc.row) {
				t.Fatalf("%q (%s) was refused for %q (%s)", tc.row.Title, tc.row.Year, tc.req.Title, tc.req.Year)
			}
		})
	}
}

func TestResolveWithProviderStreamsTheRequestedYearAmongSameTitledRows(t *testing.T) {
	rows := []media.SearchResult{}
	streams := map[string]*media.Stream{}
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("movie/remake-%d", i)
		rows = append(rows, media.SearchResult{ID: id, Title: "Foo", Type: media.Movie, Year: "2020"})
		streams[id] = &media.Stream{URL: "https://cdn/remake.m3u8"}
	}
	rows = append(rows, media.SearchResult{ID: "movie/original", Title: "Foo", Type: media.Movie, Year: "2013"}, rows[0])
	streams["movie/original"] = &media.Stream{URL: "https://cdn/original.m3u8"}
	p := &perIDSP{fakeSP: &fakeSP{searchResults: rows}, streams: streams}

	got, _, err := resolveWithProvider(p, Request{Title: "Foo", Year: "2013", MediaType: media.Movie}, func(string, ...any) {})
	if err != nil || got == nil || got.URL != "https://cdn/original.m3u8" {
		t.Fatalf("got %v / %v, want the 2013 film's stream", got, err)
	}
}
