package cmd

import (
	"testing"

	"lobster/internal/media"
)

func TestFallbackCandidatesPreferSameType(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/1", Title: "Movie", Type: media.Movie},
		{ID: "tv/1", Title: "Series", Type: media.TV},
		{ID: "tv/2", Title: "Series Alt", Type: media.TV},
	}

	got := fallbackCandidates(results, media.TV)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != "tv/1" || got[1].ID != "tv/2" {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}

func TestFallbackCandidatesLimitAndDedupe(t *testing.T) {
	results := []media.SearchResult{
		{ID: "movie/1", Title: "One", Type: media.Movie},
		{ID: "movie/1", Title: "One Duplicate", Type: media.Movie},
		{ID: "movie/2", Title: "Two", Type: media.Movie},
		{ID: "movie/3", Title: "Three", Type: media.Movie},
		{ID: "movie/4", Title: "Four", Type: media.Movie},
		{ID: "movie/5", Title: "Five", Type: media.Movie},
		{ID: "movie/6", Title: "Six", Type: media.Movie},
	}

	got := fallbackCandidates(results, media.Movie)
	if len(got) != maxFallbackCandidates {
		t.Fatalf("len(got) = %d, want %d", len(got), maxFallbackCandidates)
	}
	if got[0].ID != "movie/1" || got[1].ID != "movie/2" || got[4].ID != "movie/5" {
		t.Fatalf("unexpected candidates: %+v", got)
	}
}

func TestMergeSubtitlesDedupesByURL(t *testing.T) {
	embedded := []media.Subtitle{
		{Language: "English", Label: "English", URL: "https://subs.example/en.vtt"},
	}
	external := []media.Subtitle{
		{Language: "English", Label: "English Duplicate", URL: "https://subs.example/en.vtt"},
		{Language: "English", Label: "English SDH", URL: "subdl:https://subs.example/en-sdh.zip"},
	}

	got := mergeSubtitles(embedded, external)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Label != "English" || got[1].Label != "English SDH" {
		t.Fatalf("unexpected subtitles: %+v", got)
	}
}
