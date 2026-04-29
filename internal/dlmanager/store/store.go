// Package store provides SQLite-backed persistence for download state.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Download represents a queued or active download.
type Download struct {
	ID            int
	Title         string
	MediaTitle    string
	MediaType     string // "movie" or "tv"
	Season        int
	Episode       int
	MediaID       string // provider content ID
	EpisodeID     string // provider episode ID
	StreamURL     string
	StreamType    string // "hls" or "http"
	Referer       string
	OutputPath    string
	SubtitleURL   string
	Status        string // pending, queued, downloading, paused, completed, failed
	Error         string
	TotalBytes    int64
	DoneBytes     int64
	TotalSegments int
	DoneSegments  int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Segment represents an HLS segment tracked for resume.
type Segment struct {
	ID         int
	DownloadID int
	Idx        int
	URL        string
	Completed  bool
}

// Store wraps a SQLite database for download persistence.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS downloads (
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

CREATE TABLE IF NOT EXISTS segments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    download_id INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    url         TEXT NOT NULL,
    completed   BOOLEAN DEFAULT 0,
    UNIQUE(download_id, idx)
);
`

// Open creates or opens a SQLite database at the given path.
// Use ":memory:" for testing.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// For in-memory databases, we must limit to one connection because
	// each connection to ":memory:" creates a separate database.
	// For file-backed databases, this is still safe with WAL mode.
	db.SetMaxOpenConns(1)

	// Enable WAL mode for better concurrent read/write performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}

	// Enable foreign keys for CASCADE deletes.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertDownload adds a new download and returns its ID.
func (s *Store) InsertDownload(d *Download) (int, error) {
	res, err := s.db.Exec(`
		INSERT INTO downloads (title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Title, d.MediaTitle, d.MediaType, d.Season, d.Episode,
		d.MediaID, d.EpisodeID, d.StreamURL, d.StreamType, d.Referer,
		d.OutputPath, d.SubtitleURL, d.Status, d.Error,
		d.TotalBytes, d.DoneBytes, d.TotalSegments, d.DoneSegments,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting download: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("getting insert ID: %w", err)
	}
	return int(id), nil
}

// GetDownload retrieves a download by ID.
func (s *Store) GetDownload(id int) (*Download, error) {
	d := &Download{}
	err := s.db.QueryRow(`
		SELECT id, title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments,
			created_at, updated_at
		FROM downloads WHERE id = ?`, id,
	).Scan(
		&d.ID, &d.Title, &d.MediaTitle, &d.MediaType, &d.Season, &d.Episode,
		&d.MediaID, &d.EpisodeID, &d.StreamURL, &d.StreamType, &d.Referer,
		&d.OutputPath, &d.SubtitleURL, &d.Status, &d.Error,
		&d.TotalBytes, &d.DoneBytes, &d.TotalSegments, &d.DoneSegments,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("download %d not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("getting download: %w", err)
	}
	return d, nil
}

// ListDownloads returns all downloads ordered by creation time (FIFO).
func (s *Store) ListDownloads() ([]Download, error) {
	rows, err := s.db.Query(`
		SELECT id, title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments,
			created_at, updated_at
		FROM downloads ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing downloads: %w", err)
	}
	defer rows.Close()

	var result []Download
	for rows.Next() {
		var d Download
		if err := rows.Scan(
			&d.ID, &d.Title, &d.MediaTitle, &d.MediaType, &d.Season, &d.Episode,
			&d.MediaID, &d.EpisodeID, &d.StreamURL, &d.StreamType, &d.Referer,
			&d.OutputPath, &d.SubtitleURL, &d.Status, &d.Error,
			&d.TotalBytes, &d.DoneBytes, &d.TotalSegments, &d.DoneSegments,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning download: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// UpdateStatus sets the status and optional error message for a download.
func (s *Store) UpdateStatus(id int, status, errMsg string) error {
	res, err := s.db.Exec(`
		UPDATE downloads SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("download %d not found", id)
	}
	return nil
}

// UpdateProgress updates byte and segment progress counters.
func (s *Store) UpdateProgress(id int, doneBytes int64, doneSegments int) error {
	res, err := s.db.Exec(`
		UPDATE downloads SET done_bytes = ?, done_segments = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, doneBytes, doneSegments, id)
	if err != nil {
		return fmt.Errorf("updating progress: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("download %d not found", id)
	}
	return nil
}

// UpdateStreamInfo sets the resolved stream URL, type, and referer.
func (s *Store) UpdateStreamInfo(id int, streamURL, streamType, referer string) error {
	res, err := s.db.Exec(`
		UPDATE downloads SET stream_url = ?, stream_type = ?, referer = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, streamURL, streamType, referer, id)
	if err != nil {
		return fmt.Errorf("updating stream info: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("download %d not found", id)
	}
	return nil
}

// DeleteDownload removes a download and its segments (via CASCADE).
func (s *Store) DeleteDownload(id int) error {
	res, err := s.db.Exec("DELETE FROM downloads WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting download: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("download %d not found", id)
	}
	return nil
}

// RecoverStale finds downloads stuck in "downloading" state older than maxAge
// and resets them to "paused" so they can be resumed.
func (s *Store) RecoverStale(maxAge time.Duration) ([]Download, error) {
	cutoff := time.Now().Add(-maxAge)
	_, err := s.db.Exec(`
		UPDATE downloads SET status = 'paused', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'downloading' AND updated_at < ?`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("recovering stale downloads: %w", err)
	}

	rows, err := s.db.Query(`
		SELECT id, title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments,
			created_at, updated_at
		FROM downloads WHERE status = 'paused' ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing recovered downloads: %w", err)
	}
	defer rows.Close()

	var result []Download
	for rows.Next() {
		var d Download
		if err := rows.Scan(
			&d.ID, &d.Title, &d.MediaTitle, &d.MediaType, &d.Season, &d.Episode,
			&d.MediaID, &d.EpisodeID, &d.StreamURL, &d.StreamType, &d.Referer,
			&d.OutputPath, &d.SubtitleURL, &d.Status, &d.Error,
			&d.TotalBytes, &d.DoneBytes, &d.TotalSegments, &d.DoneSegments,
			&d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning recovered download: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// ClearCompleted removes all downloads with status "completed".
func (s *Store) ClearCompleted() error {
	_, err := s.db.Exec("DELETE FROM downloads WHERE status = 'completed'")
	if err != nil {
		return fmt.Errorf("clearing completed: %w", err)
	}
	return nil
}

// InsertSegments bulk-inserts HLS segments for a download.
func (s *Store) InsertSegments(downloadID int, segments []Segment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO segments (download_id, idx, url, completed)
		VALUES (?, ?, ?, 0)`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, seg := range segments {
		if _, err := stmt.Exec(downloadID, seg.Idx, seg.URL); err != nil {
			return fmt.Errorf("inserting segment %d: %w", seg.Idx, err)
		}
	}

	// Update total_segments count on the download.
	if _, err := tx.Exec(`
		UPDATE downloads SET total_segments = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, len(segments), downloadID); err != nil {
		return fmt.Errorf("updating total segments: %w", err)
	}

	return tx.Commit()
}

// GetSegments returns all segments for a download, ordered by index.
func (s *Store) GetSegments(downloadID int) ([]Segment, error) {
	rows, err := s.db.Query(`
		SELECT id, download_id, idx, url, completed
		FROM segments WHERE download_id = ? ORDER BY idx ASC`, downloadID)
	if err != nil {
		return nil, fmt.Errorf("getting segments: %w", err)
	}
	defer rows.Close()

	var result []Segment
	for rows.Next() {
		var seg Segment
		if err := rows.Scan(&seg.ID, &seg.DownloadID, &seg.Idx, &seg.URL, &seg.Completed); err != nil {
			return nil, fmt.Errorf("scanning segment: %w", err)
		}
		result = append(result, seg)
	}
	return result, rows.Err()
}

// MarkSegmentDone marks a specific segment as completed.
func (s *Store) MarkSegmentDone(downloadID, idx int) error {
	_, err := s.db.Exec(`
		UPDATE segments SET completed = 1
		WHERE download_id = ? AND idx = ?`, downloadID, idx)
	if err != nil {
		return fmt.Errorf("marking segment done: %w", err)
	}
	return nil
}

// CountSegments returns total and completed segment counts.
func (s *Store) CountSegments(downloadID int) (total, done int, err error) {
	err = s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(completed), 0)
		FROM segments WHERE download_id = ?`, downloadID).Scan(&total, &done)
	if err != nil {
		return 0, 0, fmt.Errorf("counting segments: %w", err)
	}
	return total, done, nil
}

// NextQueued returns the oldest download with status "queued", or nil if none.
func (s *Store) NextQueued() (*Download, error) {
	d := &Download{}
	err := s.db.QueryRow(`
		SELECT id, title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments,
			created_at, updated_at
		FROM downloads WHERE status = 'queued'
		ORDER BY created_at ASC LIMIT 1`,
	).Scan(
		&d.ID, &d.Title, &d.MediaTitle, &d.MediaType, &d.Season, &d.Episode,
		&d.MediaID, &d.EpisodeID, &d.StreamURL, &d.StreamType, &d.Referer,
		&d.OutputPath, &d.SubtitleURL, &d.Status, &d.Error,
		&d.TotalBytes, &d.DoneBytes, &d.TotalSegments, &d.DoneSegments,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting next queued: %w", err)
	}
	return d, nil
}

// ClaimNextQueued atomically finds the oldest queued download and sets its
// status to "downloading", returning it. Returns nil if no queued work exists.
func (s *Store) ClaimNextQueued() (*Download, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	d := &Download{}
	err = tx.QueryRow(`
		SELECT id, title, media_title, media_type, season, episode,
			media_id, episode_id, stream_url, stream_type, referer,
			output_path, subtitle_url, status, error,
			total_bytes, done_bytes, total_segments, done_segments,
			created_at, updated_at
		FROM downloads WHERE status = 'queued'
		ORDER BY created_at ASC LIMIT 1`,
	).Scan(
		&d.ID, &d.Title, &d.MediaTitle, &d.MediaType, &d.Season, &d.Episode,
		&d.MediaID, &d.EpisodeID, &d.StreamURL, &d.StreamType, &d.Referer,
		&d.OutputPath, &d.SubtitleURL, &d.Status, &d.Error,
		&d.TotalBytes, &d.DoneBytes, &d.TotalSegments, &d.DoneSegments,
		&d.CreatedAt, &d.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claiming next queued: %w", err)
	}

	_, err = tx.Exec(`UPDATE downloads SET status = 'downloading', updated_at = CURRENT_TIMESTAMP WHERE id = ?`, d.ID)
	if err != nil {
		return nil, fmt.Errorf("updating claimed download: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing claim: %w", err)
	}

	d.Status = "downloading"
	return d, nil
}
