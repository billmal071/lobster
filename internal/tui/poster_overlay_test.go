package tui

import (
	"io"
	"testing"

	"lobster/internal/media"
)

func TestPosterKeyChanges(t *testing.T) {
	itemA := &media.SearchResult{ID: "movie/1", Poster: "https://img/a.jpg"}
	itemB := &media.SearchResult{ID: "movie/2", Poster: "https://img/b.jpg"}
	base := AppModel{
		activeTab:   tabMovies,
		posterReady: true,
		currentItem: itemA,
		posterB64:   "abc",
		posterImgW:  2,
		posterImgH:  3,
		width:       100,
		height:      40,
	}
	k1 := base.posterKey()

	// A different poster (different URL) changes the key.
	m2 := base
	m2.currentItem = itemB
	if m2.posterKey() == k1 {
		t.Error("expected key to change when the poster (item) changes")
	}

	// Changing width changes key.
	m3 := base
	m3.width = 200
	if m3.posterKey() == k1 {
		t.Error("expected key to change when width changes")
	}

	// Changing posterReady changes key (posterVisible flips).
	m4 := base
	m4.posterReady = false
	if m4.posterKey() == k1 {
		t.Error("expected key to change when posterReady changes")
	}

	// Identical model produces same key.
	m5 := base
	if m5.posterKey() != k1 {
		t.Errorf("expected identical model to produce same key, got %q vs %q", m5.posterKey(), k1)
	}
}

func TestRedrawPosterCmdNilGuards(t *testing.T) {
	base := AppModel{
		activeTab:   tabMovies,
		posterReady: true,
		posterB64:   "abc",
		width:       100,
		height:      40,
	}

	// nil out => nil cmd.
	m1 := base
	m1.out = nil
	if m1.redrawPosterCmd(6, 1) != nil {
		t.Error("expected nil cmd when out is nil")
	}

	// out set but posterReady=false => nil cmd.
	m2 := base
	m2.out = newSyncWriter(io.Discard)
	m2.posterReady = false
	if m2.redrawPosterCmd(6, 1) != nil {
		t.Error("expected nil cmd when posterReady=false (not visible)")
	}

	// Valid state => non-nil cmd. Do not execute — just check nil vs non-nil.
	m3 := base
	m3.out = newSyncWriter(io.Discard)
	if m3.redrawPosterCmd(6, 1) == nil {
		t.Error("expected non-nil cmd when out set, posterReady=true, posterB64 non-empty")
	}
}
