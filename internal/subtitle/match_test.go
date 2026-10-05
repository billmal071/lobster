package subtitle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// subdlStub serves the two-step SubDL protocol from canned answers.
//
// Step 1 is the request carrying film_name; step 2 is the one carrying sd_id.
// It records every step-1 query so a test can assert what was asked, and
// records whether step 2 was reached at all.
type subdlStub struct {
	results   []subdlResult
	subtitles []subdlSubtitle

	searchQueries []url.Values
	subQueries    []url.Values
}

func (s *subdlStub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		if q.Get("sd_id") != "" {
			s.subQueries = append(s.subQueries, q)
			_ = json.NewEncoder(w).Encode(subdlResponse{Status: true, Subtitles: s.subtitles})
			return
		}
		s.searchQueries = append(s.searchQueries, q)
		_ = json.NewEncoder(w).Encode(subdlResponse{Status: true, Results: s.results})
	}))
	t.Cleanup(srv.Close)

	saved := subdlAPI
	subdlAPI = srv.URL
	t.Cleanup(func() { subdlAPI = saved })
	return srv
}

// TestSubDLDoesNotSubtitleAFilmWithASeries is the Fargo case, reproduced live on
// 2026-10-05: film_name=Fargo answers with the 2014 television series and
// nothing else, and a film request carries no season or episode, so every
// client-side filter in Search is gated off and the series' episode 1 is
// accepted as the film's subtitle track.
func TestSubDLDoesNotSubtitleAFilmWithASeries(t *testing.T) {
	stub := &subdlStub{
		results: []subdlResult{
			{SDId: 1300401, Type: "tv", Name: "Fargo", Year: 2014},
		},
		subtitles: []subdlSubtitle{
			{ReleaseName: "Fargo - 1x01 - The Crocodile's Dilemma.HDTV.2HD.en",
				Language: "English", URL: "/subtitle/fargo-s01e01.zip", Season: 1, Episode: 1},
		},
	}
	stub.serve(t)

	// A film: no season, no episode.
	subs, err := NewSubDL("stub-key").Search("Fargo", "en", 0, 0)
	if err == nil {
		t.Errorf("Search for the film %q against a series-only catalogue returned no error; want one", "Fargo")
	}
	if len(subs) != 0 {
		t.Errorf("Search returned %d subtitles for a film whose only SubDL match is a series; want 0. First is %q",
			len(subs), subs[0].Label)
	}
	if len(stub.subQueries) != 0 {
		t.Errorf("Search fetched subtitles for sd_id %q after SubDL said the match is a series, not a film; want no step-2 request",
			stub.subQueries[0].Get("sd_id"))
	}
}

// TestSubDLAsksSubDLForTheKindOfWorkItWants pins the parameter that actually
// gets the right sd_id. Verified live 2026-10-05: film_name=Fargo is the 2014
// series, film_name=Fargo&type=movie is the 1996 film (sd_id 213795); and
// film_name=Avatar is the 2009 film, film_name=Avatar&type=tv is
// Avatar: The Last Airbender.
func TestSubDLAsksSubDLForTheKindOfWorkItWants(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
		wantType        string
	}{
		{"film", 0, 0, "movie"},
		{"episode", 1, 1, "tv"},
		{"season only", 2, 0, "tv"},
		{"episode only", 0, 7, "tv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &subdlStub{
				results:   []subdlResult{{SDId: 42, Type: tc.wantType, Name: "Thing"}},
				subtitles: []subdlSubtitle{{ReleaseName: "Thing", Language: "English", URL: "/x.zip"}},
			}
			stub.serve(t)

			if _, err := NewSubDL("stub-key").Search("Thing", "en", tc.season, tc.episode); err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(stub.searchQueries) != 1 {
				t.Fatalf("got %d step-1 requests, want 1", len(stub.searchQueries))
			}
			if got := stub.searchQueries[0].Get("type"); got != tc.wantType {
				t.Errorf("step-1 search sent type=%q, want %q — without it SubDL picks the kind of work, not the caller", got, tc.wantType)
			}
		})
	}
}

// TestSubDLKeepsAMatchSpelledDifferently is the guard against overcorrecting.
// SubDL resolves both "KAMUI: He's Behind You" and "Ushiro no Shoumen
// Kamui-san" to sd_id 12716619 (verified live 2026-10-05), so the result name
// must not be compared with the query: a name check would reject this correct
// match and trade a rare wrong subtitle for a common missing one.
func TestSubDLKeepsAMatchSpelledDifferently(t *testing.T) {
	for _, resultType := range []string{"tv", ""} {
		t.Run("type="+resultType, func(t *testing.T) {
			stub := &subdlStub{
				results: []subdlResult{
					{SDId: 12716619, Type: resultType, Name: "KAMUI: He's Behind You", Year: 2026},
				},
				subtitles: []subdlSubtitle{
					{ReleaseName: "Kamui.Hes.Behind.You.S01E01.1080p.WEB-DL.264",
						Language: "English", URL: "/subtitle/kamui-s01e01.zip", Season: 1, Episode: 1},
				},
			}
			stub.serve(t)

			subs, err := NewSubDL("stub-key").Search("Ushiro no Shoumen Kamui-san", "en", 1, 1)
			if err != nil {
				t.Fatalf("Search for a correct match under a different romanisation failed: %v", err)
			}
			if len(subs) != 1 {
				t.Fatalf("got %d subtitles for a correct match under a different romanisation, want 1", len(subs))
			}
			if len(stub.subQueries) != 1 || stub.subQueries[0].Get("sd_id") != "12716619" {
				t.Fatalf("step-2 did not fetch sd_id 12716619: %v", stub.subQueries)
			}
		})
	}
}

// osStub serves OpenSubtitles' /subtitles from a canned payload.
func osStub(t *testing.T, payload string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	saved := openSubtitlesAPI
	openSubtitlesAPI = srv.URL
	t.Cleanup(func() { openSubtitlesAPI = saved })
}

// TestOpenSubtitlesDropsTheOtherKindOfWork is the same hazard as the SubDL
// Fargo case, in the other client: query= is fuzzy, the caller's known kind of
// work is never enforced, and Search accepts every entry it is handed. A film
// request must not come back with an episode of a same-named series.
func TestOpenSubtitlesDropsTheOtherKindOfWork(t *testing.T) {
	payload := `{"total_count":2,"data":[
	  {"attributes":{"language":"en","download_count":9,"feature_details":{"feature_type":"Episode","title":"The Crocodile's Dilemma","parent_title":"Fargo","season_number":1,"episode_number":1},"files":[{"file_id":111,"file_name":"Fargo.S01E01.srt"}]}},
	  {"attributes":{"language":"en","download_count":4,"feature_details":{"feature_type":"Movie","title":"Fargo","year":1996},"files":[{"file_id":222,"file_name":"Fargo.1996.srt"}]}}
	]}`
	osStub(t, payload)

	subs, err := NewOpenSubtitles("stub-key").Search("Fargo", "en", 0, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 {
		var got []string
		for _, s := range subs {
			got = append(got, s.URL)
		}
		t.Fatalf("film request kept %d entries, want only the Movie one; got %s", len(subs), strings.Join(got, " "))
	}
	if subs[0].URL != "opensubtitles:222" {
		t.Errorf("film request kept %s, want opensubtitles:222 — the Episode entry belongs to the series, not the film", subs[0].URL)
	}
}

// TestOpenSubtitlesEpisodeRequestDropsTheFilm is the mirror direction.
func TestOpenSubtitlesEpisodeRequestDropsTheFilm(t *testing.T) {
	payload := `{"total_count":2,"data":[
	  {"attributes":{"language":"en","feature_details":{"feature_type":"Movie","title":"Fargo","year":1996},"files":[{"file_id":222,"file_name":"Fargo.1996.srt"}]}},
	  {"attributes":{"language":"en","feature_details":{"feature_type":"Episode","title":"The Crocodile's Dilemma","parent_title":"Fargo"},"files":[{"file_id":111,"file_name":"Fargo.S01E01.srt"}]}}
	]}`
	osStub(t, payload)

	subs, err := NewOpenSubtitles("stub-key").Search("Fargo", "en", 1, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(subs) != 1 || subs[0].URL != "opensubtitles:111" {
		t.Fatalf("episode request kept %d entries (%v), want only opensubtitles:111", len(subs), subs)
	}
}

// TestOpenSubtitlesKeepsAnEntryWithNoFeatureType is the guard against
// overcorrecting: the filter must reject a stated mismatch, never a missing
// statement, so an entry carrying no feature_details still reaches the player.
func TestOpenSubtitlesKeepsAnEntryWithNoFeatureType(t *testing.T) {
	payload := `{"total_count":1,"data":[
	  {"attributes":{"language":"en","files":[{"file_id":333,"file_name":"Something.srt"}]}}
	]}`
	osStub(t, payload)

	for _, tc := range []struct{ season, episode int }{{0, 0}, {1, 1}} {
		subs, err := NewOpenSubtitles("stub-key").Search("Something", "en", tc.season, tc.episode)
		if err != nil {
			t.Fatalf("Search(s=%d,e=%d): %v", tc.season, tc.episode, err)
		}
		if len(subs) != 1 {
			t.Errorf("Search(s=%d,e=%d) dropped an entry with no feature_type; want it kept", tc.season, tc.episode)
		}
	}
}
