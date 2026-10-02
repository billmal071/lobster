package resolver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// The admission gate, against the real provider.
//
// Matches is what `episodes` uses to decide whether a fallback provider's
// answer is about the show the ref names (cmd.probeSeasons), and it demands
// that the candidate's title reduce to the same normalized key as the ref's.
// AnimeOnsen rows carry two titles that disagree —
//
//	content_title     "Ushiro no Shoumen Kamui-san"
//	content_title_en  "KAMUI: He's Behind You"
//
// — so which one the provider reports decides whether this source is usable at
// all or merely appears to do nothing: a ref found under either spelling has to
// be admitted, and a ref naming a different show has to be refused.
//
// This test lives in the resolver package rather than in provider because
// provider cannot import resolver (resolver imports provider), and restating
// Matches' rule inside a provider test would be a test of the restatement.
// NewAnimeOnsenAt exists so the real provider can be driven from here.
func TestAnimeOnsenAnswersUnderATitleMatchesWillAdmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, ok := strings.CutPrefix(r.URL.Path, "/v4/search/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// The live API indexes both spellings of this title and answers the
		// same single row for either, plus for the bare "kamui".
		_ = q
		fmt.Fprint(w, `{"status":200,"result":[
		  {"content_id":"cvYyOlmbfFWvJYWG","content_title":"Ushiro no Shoumen Kamui-san","content_title_en":"KAMUI: He's Behind You"}
		]}`)
	}))
	t.Cleanup(srv.Close)

	p := provider.NewAnimeOnsenAt(srv.URL+"/v4", srv.URL+"/video/mp4-dash")

	for _, tc := range []struct {
		name  string
		title string
		admit bool
	}{
		{"english ref", "KAMUI: He's Behind You", true},
		{"romaji ref", "Ushiro no Shoumen Kamui-san", true},
		// The guarantee-violating input. "Ninja Kamui" is a different show
		// this catalogue also holds, and a provider that echoed the query back
		// as the row's title would have Matches admit it — handing `episodes`
		// one show's episode list under another show's name, exit 0.
		{"a different show", "Ninja Kamui", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exactly what cmd.probeSeasons does: search by the ref's title,
			// rank, then gate on Matches.
			results, err := p.Search(tc.title)
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.title, err)
			}
			req := Request{Title: tc.title, MediaType: media.TV}
			admitted := false
			for _, c := range Candidates(results, req) {
				if Matches(c, req) {
					admitted = true
				}
			}
			if admitted != tc.admit {
				got := "refused"
				if admitted {
					got = "admitted"
				}
				want := "refused"
				if tc.admit {
					want = "admitted"
				}
				t.Fatalf("ref %q was %s, want %s; the provider reported %q", tc.title, got, want, titlesOf(results))
			}
		})
	}
}

func titlesOf(rs []media.SearchResult) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Title)
	}
	return out
}
