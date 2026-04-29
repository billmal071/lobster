package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lobster/internal/dlmanager/store"
)

func TestParseM3U8(t *testing.T) {
	playlist := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
seg_000.ts
#EXTINF:10.0,
seg_001.ts
#EXTINF:10.0,
seg_002.ts
#EXT-X-ENDLIST`

	urls, isMaster := parseM3U8(playlist, "https://cdn.example.com/stream/playlist.m3u8")
	if isMaster {
		t.Error("should not be master playlist")
	}
	if len(urls) != 3 {
		t.Fatalf("segment count: got %d, want 3", len(urls))
	}
	if !strings.HasSuffix(urls[0], "/stream/seg_000.ts") {
		t.Errorf("segment 0 URL: %s", urls[0])
	}
	if !strings.HasSuffix(urls[2], "/stream/seg_002.ts") {
		t.Errorf("segment 2 URL: %s", urls[2])
	}
}

func TestParseM3U8Master(t *testing.T) {
	master := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360
360p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1400000,RESOLUTION=1280x720
720p/playlist.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1920x1080
1080p/playlist.m3u8`

	urls, isMaster := parseM3U8(master, "https://cdn.example.com/master.m3u8")
	if !isMaster {
		t.Error("should be master playlist")
	}
	if len(urls) != 3 {
		t.Fatalf("variant count: got %d, want 3", len(urls))
	}
	// Last should be highest bandwidth.
	if !strings.Contains(urls[2], "1080p") {
		t.Errorf("last variant should be 1080p: %s", urls[2])
	}
}

func TestParseM3U8AbsoluteURLs(t *testing.T) {
	playlist := `#EXTM3U
#EXTINF:10.0,
https://other-cdn.example.com/seg_000.ts
#EXTINF:10.0,
https://other-cdn.example.com/seg_001.ts
#EXT-X-ENDLIST`

	urls, _ := parseM3U8(playlist, "https://cdn.example.com/playlist.m3u8")
	if len(urls) != 2 {
		t.Fatalf("count: got %d, want 2", len(urls))
	}
	if urls[0] != "https://other-cdn.example.com/seg_000.ts" {
		t.Errorf("absolute URL not preserved: %s", urls[0])
	}
}

func makeHLSServer(t *testing.T, numSegments int) *httptest.Server {
	t.Helper()
	segData := []byte("FAKE_TS_SEGMENT_DATA_1234567890") // 30 bytes

	mux := http.NewServeMux()

	// Build media playlist.
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n")
	for i := 0; i < numSegments; i++ {
		playlist.WriteString(fmt.Sprintf("#EXTINF:10.0,\nseg_%05d.ts\n", i))
	}
	playlist.WriteString("#EXT-X-ENDLIST\n")

	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte(playlist.String()))
	})

	for i := 0; i < numSegments; i++ {
		idx := i
		mux.HandleFunc(fmt.Sprintf("/seg_%05d.ts", idx), func(w http.ResponseWriter, r *http.Request) {
			w.Write(segData)
		})
	}

	return httptest.NewServer(mux)
}

func TestHLSDownload(t *testing.T) {
	srv := makeHLSServer(t, 3)
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	e := &HLSEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(context.Background(), srv.URL+"/playlist.m3u8", out, "", nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	// Output file should exist.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output file is empty")
	}

	// Parts dir should be cleaned up.
	if _, err := os.Stat(out + ".parts"); !os.IsNotExist(err) {
		t.Error("parts directory not cleaned up")
	}
}

func TestHLSResume(t *testing.T) {
	srv := makeHLSServer(t, 5)
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer s.Close()

	// Insert a download record.
	dl := &store.Download{
		Title:      "Test",
		MediaTitle: "Test",
		MediaType:  "movie",
		OutputPath: out,
		StreamURL:  srv.URL + "/playlist.m3u8",
		StreamType: "hls",
		Status:     "downloading",
	}
	id, _ := s.InsertDownload(dl)

	// Insert segments and mark first 2 as done.
	segs := make([]store.Segment, 5)
	for i := 0; i < 5; i++ {
		segs[i] = store.Segment{Idx: i, URL: fmt.Sprintf("%s/seg_%05d.ts", srv.URL, i)}
	}
	s.InsertSegments(id, segs)
	s.MarkSegmentDone(id, 0)
	s.MarkSegmentDone(id, 1)

	// Pre-create the first 2 segment files.
	partsDir := out + ".parts"
	os.MkdirAll(partsDir, 0755)
	os.WriteFile(filepath.Join(partsDir, "seg_00000.ts"), []byte("FAKE_TS_SEGMENT_DATA_1234567890"), 0644)
	os.WriteFile(filepath.Join(partsDir, "seg_00001.ts"), []byte("FAKE_TS_SEGMENT_DATA_1234567890"), 0644)

	// Track which segments are actually downloaded.
	var fetched int32
	origClient := srv.Client()
	countingTransport := &countingRoundTripper{
		inner:   origClient.Transport,
		fetched: &fetched,
	}
	countClient := &http.Client{Transport: countingTransport}

	e := &HLSEngine{Client: countClient, Store: s, RetryDelay: 1 * time.Millisecond}
	err = e.ResumeWithID(context.Background(), id, srv.URL+"/playlist.m3u8", out, "", nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Should have fetched playlist + 3 remaining segments = 4 requests (not 5 segments).
	got := atomic.LoadInt32(&fetched)
	// The playlist fetch counts as 1, plus 3 segment fetches.
	if got != 4 {
		t.Errorf("requests made: got %d, want 4 (1 playlist + 3 segments)", got)
	}
}

type countingRoundTripper struct {
	inner   http.RoundTripper
	fetched *int32
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(c.fetched, 1)
	if c.inner != nil {
		return c.inner.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestHLSSegmentProgress(t *testing.T) {
	srv := makeHLSServer(t, 4)
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	var progressCalls int32
	var lastDone int64

	e := &HLSEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(context.Background(), srv.URL+"/playlist.m3u8", out, "", func(done, total int64) {
		atomic.AddInt32(&progressCalls, 1)
		if total != 4 {
			t.Errorf("total: got %d, want 4", total)
		}
		if done < atomic.LoadInt64(&lastDone) {
			t.Errorf("progress went backwards: %d < %d", done, lastDone)
		}
		atomic.StoreInt64(&lastDone, done)
	})
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	if atomic.LoadInt32(&progressCalls) != 4 {
		t.Errorf("progress calls: got %d, want 4", atomic.LoadInt32(&progressCalls))
	}
}

func TestHLSCancel(t *testing.T) {
	// Create a server where segment 2 blocks forever.
	segData := []byte("FAKE_TS_SEGMENT_DATA_1234567890")
	blocked := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:10.0,\nseg_00000.ts\n#EXTINF:10.0,\nseg_00001.ts\n#EXTINF:10.0,\nseg_00002.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/seg_00000.ts", func(w http.ResponseWriter, r *http.Request) { w.Write(segData) })
	mux.HandleFunc("/seg_00001.ts", func(w http.ResponseWriter, r *http.Request) { w.Write(segData) })
	mux.HandleFunc("/seg_00002.ts", func(w http.ResponseWriter, r *http.Request) {
		close(blocked)
		<-r.Context().Done() // Block until cancelled.
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-blocked
		cancel()
	}()

	e := &HLSEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(ctx, srv.URL+"/playlist.m3u8", out, "", nil)
	if err == nil {
		t.Fatal("expected error from cancellation")
	}

	// Parts dir should exist with 2 completed segments.
	partsDir := out + ".parts"
	entries, _ := os.ReadDir(partsDir)
	// Should have at least seg_00000.ts and seg_00001.ts.
	tsCount := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".ts") {
			tsCount++
		}
	}
	if tsCount < 2 {
		t.Errorf("expected at least 2 segment files, got %d", tsCount)
	}
}

func TestHLSMasterPlaylist(t *testing.T) {
	segData := []byte("FAKE_TS_SEGMENT_DATA_1234567890")

	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\n360p/playlist.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2800000\n1080p/playlist.m3u8\n")
	})
	mux.HandleFunc("/1080p/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:10.0,\nseg_00000.ts\n#EXT-X-ENDLIST\n")
	})
	mux.HandleFunc("/1080p/seg_00000.ts", func(w http.ResponseWriter, r *http.Request) {
		w.Write(segData)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "output.mkv")

	e := &HLSEngine{Client: srv.Client(), RetryDelay: 1 * time.Millisecond}
	err := e.Download(context.Background(), srv.URL+"/master.m3u8", out, "", nil)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	info, _ := os.Stat(out)
	if info.Size() == 0 {
		t.Error("output is empty")
	}
}
