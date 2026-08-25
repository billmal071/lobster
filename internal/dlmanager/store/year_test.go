package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The release year is what lets the stream resolver tell a franchise entry
// apart from its sequels, so a queued download has to carry it across a
// restart — resolution happens later, in a different process run.
func TestDownloadYearRoundTrips(t *testing.T) {
	s := openTestStore(t)

	d := sampleDownload()
	d.Year = "2002"
	id, err := s.InsertDownload(d)
	if err != nil {
		t.Fatalf("InsertDownload: %v", err)
	}

	got, err := s.GetDownload(id)
	if err != nil {
		t.Fatalf("GetDownload: %v", err)
	}
	if got.Year != "2002" {
		t.Errorf("Year = %q, want 2002", got.Year)
	}

	list, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(list) != 1 || list[0].Year != "2002" {
		t.Errorf("ListDownloads lost the year: %+v", list)
	}
}

// Existing installs already have a downloads table without the year column.
// Open must add it rather than failing or silently reading a table it cannot
// scan — the schema is created with CREATE TABLE IF NOT EXISTS, which is a
// no-op against an old database.
func TestOpenAddsYearColumnToExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// Build a pre-year database by hand.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE downloads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL, media_title TEXT NOT NULL, media_type TEXT NOT NULL,
		season INTEGER DEFAULT 0, episode INTEGER DEFAULT 0,
		media_id TEXT NOT NULL DEFAULT '', episode_id TEXT NOT NULL DEFAULT '',
		stream_url TEXT NOT NULL DEFAULT '', stream_type TEXT NOT NULL DEFAULT '',
		referer TEXT DEFAULT '', output_path TEXT NOT NULL, subtitle_url TEXT DEFAULT '',
		status TEXT NOT NULL DEFAULT 'queued', error TEXT DEFAULT '',
		total_bytes INTEGER DEFAULT 0, done_bytes INTEGER DEFAULT 0,
		total_segments INTEGER DEFAULT 0, done_segments INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO downloads (title, media_title, media_type, output_path)
		VALUES ('old', 'old', 'movie', '/tmp/old.mkv')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-year database: %v", err)
	}
	defer s.Close()

	list, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("ListDownloads after migration: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d, want 1", len(list))
	}
	if list[0].Year != "" {
		t.Errorf("migrated row Year = %q, want empty", list[0].Year)
	}

	// And the column is usable going forward.
	d := sampleDownload()
	d.Year = "1994"
	id, err := s.InsertDownload(d)
	if err != nil {
		t.Fatalf("InsertDownload after migration: %v", err)
	}
	got, err := s.GetDownload(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Year != "1994" {
		t.Errorf("Year = %q, want 1994", got.Year)
	}
}
