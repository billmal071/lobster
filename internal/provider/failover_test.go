package provider

import "testing"

func TestMergeOverrides(t *testing.T) {
	base := map[string][]string{"flixhq": {"flixhq.to"}}
	extra := map[string][]string{"flixhq": {"flixhq.to", "flixhq.dad"}, "tbcpl": {"1shows.org"}}
	got := MergeOverrides(base, extra)
	if len(got["flixhq"]) != 2 || got["flixhq"][1] != "flixhq.dad" {
		t.Fatalf("flixhq merge wrong: %v", got["flixhq"])
	}
	if got["tbcpl"][0] != "1shows.org" {
		t.Fatalf("tbcpl merge wrong: %v", got["tbcpl"])
	}
}
