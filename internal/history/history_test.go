package history

import (
	"os"
	"path/filepath"
	"testing"

	"lobster/internal/media"
)

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Create the lobster data dir
	dataDir := filepath.Join(tmpDir, "lobster")
	os.MkdirAll(dataDir, 0700)

	entry := media.HistoryEntry{
		ID:       "movie/test-movie-123",
		Title:    "Test Movie",
		Type:     media.Movie,
		Season:   0,
		Episode:  0,
		Position: 1234,
		Duration: 5678,
	}

	if err := Save(entry); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	entries, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	got := entries[0]
	if got.ID != entry.ID {
		t.Errorf("ID = %q, want %q", got.ID, entry.ID)
	}
	if got.Title != entry.Title {
		t.Errorf("Title = %q, want %q", got.Title, entry.Title)
	}
	if got.Position != entry.Position {
		t.Errorf("Position = %f, want %f", got.Position, entry.Position)
	}
}

func TestSaveUpdatesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "lobster"), 0700)

	entry := media.HistoryEntry{
		ID:       "movie/test-123",
		Title:    "Test",
		Type:     media.Movie,
		Position: 100,
	}
	Save(entry)

	// Update position
	entry.Position = 500
	Save(entry)

	entries, _ := Load()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(entries))
	}
	if entries[0].Position != 500 {
		t.Errorf("position = %f, want 500", entries[0].Position)
	}
}

func TestRemove(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, "lobster"), 0700)

	Save(media.HistoryEntry{ID: "a", Title: "A", Type: media.Movie})
	Save(media.HistoryEntry{ID: "b", Title: "B", Type: media.Movie})

	Remove("a", 0, 0)

	entries, _ := Load()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after remove, got %d", len(entries))
	}
	if entries[0].ID != "b" {
		t.Errorf("remaining entry ID = %q, want b", entries[0].ID)
	}
}

func TestFormatForDisplay(t *testing.T) {
	entries := []media.HistoryEntry{
		{Title: "Movie A", Type: media.Movie, Position: 500, Duration: 1000},
		{Title: "Show B", Type: media.TV, Season: 2, Episode: 5, Position: 0},
	}

	items := FormatForDisplay(entries)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0] != "Movie A [50%]" {
		t.Errorf("movie display = %q, want 'Movie A [50%%]'", items[0])
	}
	if items[1] != "Show B S02E05" {
		t.Errorf("tv display = %q, want 'Show B S02E05'", items[1])
	}
}

func TestFormatLine(t *testing.T) {
	entry := media.HistoryEntry{
		ID:       "movie/test-123",
		Title:    "Test Movie",
		Type:     media.Movie,
		Season:   0,
		Episode:  0,
		Position: 100,
		Duration: 200,
	}

	line := formatLine(entry)
	expected := "movie/test-123\tTest Movie\tmovie\t0\t0\t100\t200"
	if line != expected {
		t.Errorf("formatLine = %q, want %q", line, expected)
	}

	// Round-trip
	parsed, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine error: %v", err)
	}
	if parsed.ID != entry.ID || parsed.Title != entry.Title || parsed.Position != entry.Position {
		t.Errorf("round-trip failed: got %+v", parsed)
	}
}
