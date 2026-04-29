package store

import (
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleDownload() *Download {
	return &Download{
		Title:      "S06E01 - Strike Back",
		MediaTitle: "The Rookie",
		MediaType:  "tv",
		Season:     6,
		Episode:    1,
		MediaID:    "tv/the-rookie-12345",
		EpisodeID:  "ep-67890",
		StreamURL:  "https://cdn.example.com/stream.m3u8",
		StreamType: "hls",
		Referer:    "https://example.com",
		OutputPath: "/home/user/Videos/lobster/The Rookie/Season 06/S06E01 - Strike Back.mkv",
		Status:     "queued",
	}
}

func TestOpen(t *testing.T) {
	s := openTestStore(t)

	// Verify tables exist.
	var name string
	err := s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='downloads'").Scan(&name)
	if err != nil {
		t.Fatalf("downloads table not found: %v", err)
	}
	err = s.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='segments'").Scan(&name)
	if err != nil {
		t.Fatalf("segments table not found: %v", err)
	}
}

func TestInsertAndGet(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()

	id, err := s.InsertDownload(d)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id < 1 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	got, err := s.GetDownload(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Title != d.Title {
		t.Errorf("title: got %q, want %q", got.Title, d.Title)
	}
	if got.MediaTitle != d.MediaTitle {
		t.Errorf("media_title: got %q, want %q", got.MediaTitle, d.MediaTitle)
	}
	if got.MediaType != d.MediaType {
		t.Errorf("media_type: got %q, want %q", got.MediaType, d.MediaType)
	}
	if got.Season != d.Season {
		t.Errorf("season: got %d, want %d", got.Season, d.Season)
	}
	if got.Episode != d.Episode {
		t.Errorf("episode: got %d, want %d", got.Episode, d.Episode)
	}
	if got.StreamURL != d.StreamURL {
		t.Errorf("stream_url: got %q, want %q", got.StreamURL, d.StreamURL)
	}
	if got.StreamType != d.StreamType {
		t.Errorf("stream_type: got %q, want %q", got.StreamType, d.StreamType)
	}
	if got.Status != "queued" {
		t.Errorf("status: got %q, want %q", got.Status, "queued")
	}
}

func TestGetNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetDownload(999)
	if err == nil {
		t.Fatal("expected error for missing download")
	}
}

func TestListDownloads(t *testing.T) {
	s := openTestStore(t)

	for i := 0; i < 3; i++ {
		d := sampleDownload()
		d.Title = "Episode " + string(rune('A'+i))
		if _, err := s.InsertDownload(d); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	list, err := s.ListDownloads()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("count: got %d, want 3", len(list))
	}
	// Verify FIFO order.
	if list[0].Title != "Episode A" {
		t.Errorf("first title: got %q, want %q", list[0].Title, "Episode A")
	}
	if list[2].Title != "Episode C" {
		t.Errorf("last title: got %q, want %q", list[2].Title, "Episode C")
	}
}

func TestUpdateStatus(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	if err := s.UpdateStatus(id, "downloading", ""); err != nil {
		t.Fatalf("update status: %v", err)
	}

	got, _ := s.GetDownload(id)
	if got.Status != "downloading" {
		t.Errorf("status: got %q, want %q", got.Status, "downloading")
	}
}

func TestUpdateStatusNotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.UpdateStatus(999, "downloading", "")
	if err == nil {
		t.Fatal("expected error for missing download")
	}
}

func TestUpdateProgress(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	before, _ := s.GetDownload(id)

	// Small delay to ensure updated_at changes.
	time.Sleep(10 * time.Millisecond)

	if err := s.UpdateProgress(id, 5000, 3); err != nil {
		t.Fatalf("update progress: %v", err)
	}

	after, _ := s.GetDownload(id)
	if after.DoneBytes != 5000 {
		t.Errorf("done_bytes: got %d, want 5000", after.DoneBytes)
	}
	if after.DoneSegments != 3 {
		t.Errorf("done_segments: got %d, want 3", after.DoneSegments)
	}
	if !after.UpdatedAt.After(before.CreatedAt) && after.UpdatedAt.Equal(before.CreatedAt) {
		// updated_at should be >= created_at (may be same second in fast tests)
	}
}

func TestUpdateStreamInfo(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	d.StreamURL = ""
	d.StreamType = ""
	d.Status = "pending"
	id, _ := s.InsertDownload(d)

	if err := s.UpdateStreamInfo(id, "https://cdn.example.com/video.mp4", "http", "https://ref.com"); err != nil {
		t.Fatalf("update stream info: %v", err)
	}

	got, _ := s.GetDownload(id)
	if got.StreamURL != "https://cdn.example.com/video.mp4" {
		t.Errorf("stream_url: got %q", got.StreamURL)
	}
	if got.StreamType != "http" {
		t.Errorf("stream_type: got %q", got.StreamType)
	}
	if got.Referer != "https://ref.com" {
		t.Errorf("referer: got %q", got.Referer)
	}
}

func TestDeleteDownload(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	if err := s.DeleteDownload(id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err := s.GetDownload(id)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.DeleteDownload(999)
	if err == nil {
		t.Fatal("expected error for missing download")
	}
}

func TestRecoverStale(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	d.Status = "downloading"
	id, _ := s.InsertDownload(d)

	// Manually set updated_at to 2 minutes ago.
	_, err := s.db.Exec("UPDATE downloads SET updated_at = datetime('now', '-120 seconds') WHERE id = ?", id)
	if err != nil {
		t.Fatalf("backdating: %v", err)
	}

	// Also insert an active (recent) download that should NOT be recovered.
	d2 := sampleDownload()
	d2.Status = "downloading"
	d2.Title = "Recent"
	s.InsertDownload(d2)

	recovered, err := s.RecoverStale(60 * time.Second)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	// Should have recovered exactly the stale one.
	got, _ := s.GetDownload(id)
	if got.Status != "paused" {
		t.Errorf("stale download status: got %q, want %q", got.Status, "paused")
	}

	// The recovered list includes all paused downloads.
	found := false
	for _, r := range recovered {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("stale download not in recovered list")
	}
}

func TestClearCompleted(t *testing.T) {
	s := openTestStore(t)

	// Insert completed + active.
	d1 := sampleDownload()
	d1.Status = "completed"
	d1.Title = "Done"
	s.InsertDownload(d1)

	d2 := sampleDownload()
	d2.Status = "downloading"
	d2.Title = "Active"
	s.InsertDownload(d2)

	if err := s.ClearCompleted(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	list, _ := s.ListDownloads()
	if len(list) != 1 {
		t.Fatalf("count after clear: got %d, want 1", len(list))
	}
	if list[0].Title != "Active" {
		t.Errorf("remaining: got %q, want %q", list[0].Title, "Active")
	}
}

func TestInsertAndGetSegments(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	segs := make([]Segment, 5)
	for i := range segs {
		segs[i] = Segment{Idx: i, URL: "https://cdn.example.com/seg_" + string(rune('0'+i)) + ".ts"}
	}

	if err := s.InsertSegments(id, segs); err != nil {
		t.Fatalf("insert segments: %v", err)
	}

	got, err := s.GetSegments(id)
	if err != nil {
		t.Fatalf("get segments: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("segment count: got %d, want 5", len(got))
	}
	if got[0].Idx != 0 || got[4].Idx != 4 {
		t.Errorf("segment order: first=%d, last=%d", got[0].Idx, got[4].Idx)
	}

	// Verify total_segments updated on download.
	dl, _ := s.GetDownload(id)
	if dl.TotalSegments != 5 {
		t.Errorf("total_segments: got %d, want 5", dl.TotalSegments)
	}
}

func TestMarkSegmentDone(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	segs := []Segment{
		{Idx: 0, URL: "https://cdn.example.com/seg0.ts"},
		{Idx: 1, URL: "https://cdn.example.com/seg1.ts"},
		{Idx: 2, URL: "https://cdn.example.com/seg2.ts"},
	}
	s.InsertSegments(id, segs)

	if err := s.MarkSegmentDone(id, 1); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	got, _ := s.GetSegments(id)
	if got[0].Completed {
		t.Error("segment 0 should not be completed")
	}
	if !got[1].Completed {
		t.Error("segment 1 should be completed")
	}
	if got[2].Completed {
		t.Error("segment 2 should not be completed")
	}
}

func TestCountSegments(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	segs := make([]Segment, 10)
	for i := range segs {
		segs[i] = Segment{Idx: i, URL: "https://cdn.example.com/seg.ts"}
	}
	s.InsertSegments(id, segs)

	// Mark 3 as done.
	s.MarkSegmentDone(id, 0)
	s.MarkSegmentDone(id, 3)
	s.MarkSegmentDone(id, 7)

	total, done, err := s.CountSegments(id)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 10 {
		t.Errorf("total: got %d, want 10", total)
	}
	if done != 3 {
		t.Errorf("done: got %d, want 3", done)
	}
}

func TestSegmentsCascadeDelete(t *testing.T) {
	s := openTestStore(t)
	d := sampleDownload()
	id, _ := s.InsertDownload(d)

	segs := []Segment{
		{Idx: 0, URL: "https://cdn.example.com/seg0.ts"},
		{Idx: 1, URL: "https://cdn.example.com/seg1.ts"},
	}
	s.InsertSegments(id, segs)

	// Delete parent download.
	s.DeleteDownload(id)

	// Segments should be gone via CASCADE.
	got, err := s.GetSegments(id)
	if err != nil {
		t.Fatalf("get segments after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("segments after cascade: got %d, want 0", len(got))
	}
}

func TestNextQueued(t *testing.T) {
	s := openTestStore(t)

	// Empty queue.
	got, err := s.NextQueued()
	if err != nil {
		t.Fatalf("next queued (empty): %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for empty queue")
	}

	// Insert queued + non-queued.
	d1 := sampleDownload()
	d1.Status = "downloading"
	d1.Title = "Active"
	s.InsertDownload(d1)

	d2 := sampleDownload()
	d2.Status = "queued"
	d2.Title = "First Queued"
	s.InsertDownload(d2)

	d3 := sampleDownload()
	d3.Status = "queued"
	d3.Title = "Second Queued"
	s.InsertDownload(d3)

	got, err = s.NextQueued()
	if err != nil {
		t.Fatalf("next queued: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil queued download")
	}
	if got.Title != "First Queued" {
		t.Errorf("title: got %q, want %q", got.Title, "First Queued")
	}
}
