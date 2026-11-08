package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

const previousReleaseSchema = `
CREATE TABLE downloads (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    title          TEXT NOT NULL,
    media_title    TEXT NOT NULL,
    media_type     TEXT NOT NULL,
    season         INTEGER DEFAULT 0,
    episode        INTEGER DEFAULT 0,
    media_id       TEXT NOT NULL DEFAULT '',
    episode_id     TEXT NOT NULL DEFAULT '',
    stream_url     TEXT NOT NULL DEFAULT '',
    stream_type    TEXT NOT NULL DEFAULT '',
    referer        TEXT DEFAULT '',
    output_path    TEXT NOT NULL,
    subtitle_url   TEXT DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'queued',
    error          TEXT DEFAULT '',
    total_bytes    INTEGER DEFAULT 0,
    done_bytes     INTEGER DEFAULT 0,
    total_segments INTEGER DEFAULT 0,
    done_segments  INTEGER DEFAULT 0,
    created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE segments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    download_id INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    url         TEXT NOT NULL,
    completed   BOOLEAN DEFAULT 0,
    UNIQUE(download_id, idx)
);
INSERT INTO downloads (title, media_title, media_type, output_path, referer, status)
VALUES ('Old', 'Old Show', 'tv', '/tmp/old.mkv', 'https://ref/', 'queued');
`

func oldSchemaDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "downloads.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(previousReleaseSchema); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return path
}

func TestOpenMigratesThePreviousReleaseSchemaKeepingRows(t *testing.T) {
	s, err := Open(oldSchemaDB(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	list, err := s.ListDownloads()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDownloads = %v, %v; want the one old row", list, err)
	}
	if list[0].Title != "Old" || list[0].Referer != "https://ref/" || list[0].Year != "" || list[0].UserAgent != "" {
		t.Fatalf("row = %+v", list[0])
	}
}

func TestConcurrentOpenOfAnOldSchemaDatabaseBothSucceed(t *testing.T) {
	path := oldSchemaDB(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path)
			if err == nil {
				defer s.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Open #%d: %v", i, err)
		}
	}
}

func TestAddColumnToleratesAColumnAlreadyAdded(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE downloads (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	const ddl = "ALTER TABLE downloads ADD COLUMN user_agent TEXT NOT NULL DEFAULT ''"
	if err := addColumn(db, ddl); err != nil {
		t.Fatal(err)
	}
	if err := addColumn(db, ddl); err != nil {
		t.Fatalf("second add = %v, want the duplicate column tolerated", err)
	}
}
