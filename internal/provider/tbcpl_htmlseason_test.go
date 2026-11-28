package provider

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func serveSPAForSeasons(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/tv/") {
		return false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, "\n<!DOCTYPE html><html><body><div id=root></div></body></html>")
	return true
}

func TestTBCPLSeasonsNameAnHTMLAnswerInsteadOfAJSONParseError(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if !serveSPAForSeasons(w, r) {
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	_, seasonsErr := p.GetSeasons("tv/219")
	_, episodesErr := p.GetEpisodes("tv/219", "219:2")
	for name, err := range map[string]error{"GetSeasons": seasonsErr, "GetEpisodes": episodesErr} {
		if err == nil {
			t.Fatalf("%s succeeded on an HTML page", name)
		}
		if strings.Contains(err.Error(), "invalid character") || !strings.Contains(err.Error(), "HTML page") {
			t.Errorf("%s error = %q, want it to say the season API answered with an HTML page", name, err)
		}
	}
}

func TestTBCPLWatchesAnEpisodeWhoseSeasonAPIAnswersHTML(t *testing.T) {
	var seasonAsks int
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if serveSPAForSeasons(w, r) {
			seasonAsks++
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/streams/languages/tv/219/2/15":
			fmt.Fprint(w, `{"languages":[]}`)
		case r.URL.Path == "/subs/tv/219/2/15":
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/streams/tv/219/2/15" && r.URL.Query().Get("s") == "peak":
			fmt.Fprint(w, `{"language":"Auto","url":"https://vercetti.example.test/tv/x/master.m3u8","headers":{}}`)
		default:
			http.Error(w, "error code: 502", http.StatusBadGateway)
		}
	})
	defer cleanup()

	if _, err := p.GetSeasons("tv/219"); err == nil {
		t.Fatal("GetSeasons succeeded on an HTML page")
	}
	stream, err := p.Watch("tv/219", "219:2:15", "", "1080")
	if err != nil || stream == nil || stream.URL == "" {
		t.Fatalf("Watch = (%v, %v), want the peak stream", stream, err)
	}
	if seasonAsks != 1 {
		t.Errorf("season API asked %d times, want 1: Watch needs no season list", seasonAsks)
	}
}
