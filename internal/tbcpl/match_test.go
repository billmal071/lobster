package tbcpl

import (
	"reflect"
	"testing"
)

func TestProviderFor(t *testing.T) {
	cases := map[string]string{
		"flixhq.ws":          "flixhqws",
		"www.flixhq.to":      "flixhq",
		"www.1shows.org":     "",
		"1flex.org":          "",
		"soap2day.example":   "",
		"kimcartoon.com.rs":  "kimcartoon",
		"allanime.to":        "",
		"totallyunknown.xyz": "",
		// Lookalike hosts must NOT match on embedded substrings.
		"evil-flixhq.ws.example": "",
		"notflixhq.example":      "",
		"flixhq.dad":             "flixhq",
	}
	for host, want := range cases {
		got, ok := ProviderFor(host)
		if want == "" && ok {
			t.Errorf("%s: expected no match, got %q", host, got)
		}
		if want != "" && got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
}

func TestMirrorDomains(t *testing.T) {
	sites := []Site{
		{URL: "https://flixhq.dad/", Category: "movies"},
		{URL: "https://www.1shows.org/", Category: "movies"},
		{URL: "https://randomsite.xyz/", Category: "movies"},
	}
	got := MirrorDomains(sites)
	want := map[string][]string{
		"flixhq": {"flixhq.dad"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MirrorDomains = %+v, want %+v", got, want)
	}
}

func TestLivePlaylists(t *testing.T) {
	c := &Catalog{Sites: []Site{
		{URL: "https://a.example/list.m3u8", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://b.example/get.php?type=m3u_plus", Category: "livetv", Enabled: true, Status: "trusted"},
		{URL: "https://c.example/watch", Category: "livetv", Enabled: true, Status: "trusted"},     // not a playlist
		{URL: "https://d.example/list.m3u8", Category: "livetv", Enabled: true, Status: ""},        // untrusted
		{URL: "https://e.example/list.m3u8", Category: "movies", Enabled: true, Status: "trusted"}, // wrong category
	}}
	got := c.LivePlaylists(false)
	want := []string{"https://a.example/list.m3u8", "https://b.example/get.php?type=m3u_plus"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LivePlaylists(false) = %v, want %v", got, want)
	}
	if len(c.LivePlaylists(true)) != 3 {
		t.Fatalf("LivePlaylists(true) want 3 (adds untrusted d)")
	}
}
