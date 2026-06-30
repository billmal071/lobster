package tui

import (
	"testing"

	"lobster/internal/media"
)

func TestFilterLiveRows(t *testing.T) {
	master := []liveRow{
		{result: media.SearchResult{Title: "A Spor"}},
		{result: media.SearchResult{Title: "BBC News"}},
		{result: media.SearchResult{Title: "Sport TV"}},
	}
	if got := filterLiveRows(master, ""); len(got) != 3 {
		t.Fatalf("empty query keeps all, got %d", len(got))
	}
	got := filterLiveRows(master, "spor")
	if len(got) != 2 {
		t.Fatalf("want 2 matches for 'spor', got %d", len(got))
	}
}
