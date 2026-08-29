package tbcpl

import (
	"reflect"
	"testing"
)

func TestProviderFor(t *testing.T) {
	cases := map[string]string{
		"flixhq.ws":            "flixhqws",
		"www.flixhq.to":        "flixhq",
		"www.1shows.org":       "tbcpl",
		"1flex.org":            "tbcpl",
		"soap2day.example":     "soap2day",
		"kimcartoon.com.rs":    "kimcartoon",
		"allanime.to":          "allanime",
		"totallyunknown.xyz":   "",
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
		"tbcpl":  {"www.1shows.org"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MirrorDomains = %+v, want %+v", got, want)
	}
}
