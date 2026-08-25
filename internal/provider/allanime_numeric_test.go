package provider

import (
	"strings"
	"testing"
)

// The fallback resolver reconstructs episode IDs as "showID:season:episode"
// (and passes "" for movies); Watch must accept those alongside native
// "showID|episodeString|trans" IDs.
func TestAllAnimeWatchNumericEpisodeID(t *testing.T) {
	plain := `{"episode":{"sourceUrls":[{"sourceUrl":"--175b54575b53","sourceName":"S-mp4","priority":9}]}}`
	blob := makeBlob(plain, "Xot36i3lK3:v1")
	routes := map[string]string{
		`"showId":"ReH"`: `{"data":{"show":{"_id":"ReH","availableEpisodesDetail":{"sub":["2","1"],"dub":[],"raw":[]}}}}`,
		`episodeString`:  `{"data":{"tobeparsed":"` + blob + `"}}`,
		`/clock.json`:    `{"links":[{"link":"https://cdn/master.m3u8"}]}`,
	}

	t.Run("resolver-style show:season:episode", func(t *testing.T) {
		a := newTestAllAnime(routes)
		st, err := a.Watch("ReH", "ReH:1:2", "Default", "1080")
		if err != nil {
			t.Fatal(err)
		}
		if st.URL != "https://cdn/master.m3u8" {
			t.Fatalf("stream URL = %q", st.URL)
		}
	})

	t.Run("empty episode ID resolves episode 1", func(t *testing.T) {
		a := newTestAllAnime(routes)
		st, err := a.Watch("ReH", "", "Default", "1080")
		if err != nil {
			t.Fatal(err)
		}
		if st.URL != "https://cdn/master.m3u8" {
			t.Fatalf("stream URL = %q", st.URL)
		}
	})

	t.Run("season beyond 1 is rejected", func(t *testing.T) {
		a := newTestAllAnime(routes)
		if _, err := a.Watch("ReH", "ReH:2:5", "Default", "1080"); err == nil || !strings.Contains(err.Error(), "season") {
			t.Fatalf("want season rejection, got %v", err)
		}
	})

	t.Run("episode not in catalog", func(t *testing.T) {
		a := newTestAllAnime(routes)
		if _, err := a.Watch("ReH", "ReH:1:9", "Default", "1080"); err == nil {
			t.Fatal("want error for missing episode")
		}
	})

	t.Run("malformed ID", func(t *testing.T) {
		a := newTestAllAnime(routes)
		if _, err := a.Watch("ReH", "ReH:x:2", "Default", "1080"); err == nil {
			t.Fatal("want error for malformed ID")
		}
	})
}

// AllAnime often indexes only the romaji or base title, so a full english
// title like "KAMUI: He's Behind You" finds nothing. Search must retry with
// the pre-colon base title and keep only results matching the original query.
func TestAllAnimeSearchColonFallback(t *testing.T) {
	a := newTestAllAnime(map[string]string{
		`KAMUI: He`: `{"data":{"shows":{"edges":[]}}}`,
		`"query":"KAMUI"`: `{"data":{"shows":{"edges":[
			{"_id":"ninja","name":"Ninja Kamui","englishName":"Ninja Kamui","availableEpisodes":{"sub":13}},
			{"_id":"kam","name":"Ushiro no Shoumen Kamui-san","englishName":"KAMUI: He's Behind You","availableEpisodes":{"sub":6}},
			{"_id":"gk","name":"Golden Kamuy","englishName":"Golden Kamuy","availableEpisodes":{"sub":12}}
		]}}}`,
	})
	res, err := a.Search("KAMUI: He's Behind You")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "kam" {
		t.Fatalf("want only the matching show, got %+v", res)
	}
}
