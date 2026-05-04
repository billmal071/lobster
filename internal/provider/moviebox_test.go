package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
)

// ---------------------------------------------------------------------------
// Test data
// ---------------------------------------------------------------------------

const testSearchResponse = `{
  "list": [
    {"subjectId": 1857349212451623008, "detailPath": "KHgp5s6Gcd2", "name": "Project Hail Mary", "subjectType": 1, "releaseYear": 2025, "seasonNum": 0, "episodeNum": 0},
    {"subjectId": 2857349212451623009, "detailPath": "ABcd5s6Gcd3", "name": "Breaking Bad", "subjectType": 2, "releaseYear": 2008, "seasonNum": 5, "episodeNum": 62}
  ],
  "total": 2
}`

const testDetailResponse = `{
  "subjectId": 1857349212451623008,
  "name": "Project Hail Mary",
  "description": "A lone astronaut must save the earth.",
  "subjectType": 1,
  "releaseYear": 2025,
  "score": "8.5",
  "area": "USA",
  "tagNames": ["Sci-Fi", "Adventure"],
  "actors": ["Ryan Gosling", "Eva Mendes"],
  "duration": "124 min"
}`

const testSeasonInfoResponse = `{
  "subjectId": 2857349212451623009,
  "name": "Breaking Bad",
  "subjectType": 2,
  "seasonList": [
    {"seasonId": 1001, "seasonNum": 1, "seasonName": "Season 1", "episodeNum": 7},
    {"seasonId": 1002, "seasonNum": 2, "seasonName": "Season 2", "episodeNum": 13}
  ]
}`

const testPlayInfoResponse = `{
  "hls": [
    {"id": "abc123", "url": "https://macdn.aoneroom.com/stream/index.m3u8", "quality": "1080p"},
    {"id": "abc124", "url": "https://macdn.aoneroom.com/stream/720.m3u8", "quality": "720p"}
  ],
  "streams": [
    {"id": "def456", "url": "https://macdn.aoneroom.com/stream/video.mp4", "quality": "720p"}
  ],
  "hasResource": true
}`

// ---------------------------------------------------------------------------
// Test server
// ---------------------------------------------------------------------------

func newMovieBoxTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/wefeed-mobile-bff/tab-operating", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testSearchResponse))
	})

	mux.HandleFunc("/wefeed-mobile-bff/subject-api/search/v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-user", "test-bearer-token")
		w.Write([]byte(testSearchResponse))
	})

	mux.HandleFunc("/wefeed-mobile-bff/subject-api/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testDetailResponse))
	})

	mux.HandleFunc("/wefeed-mobile-bff/subject-api/season-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testSeasonInfoResponse))
	})

	mux.HandleFunc("/wefeed-mobile-bff/subject-api/play-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testPlayInfoResponse))
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newMovieBoxForTest(srv *httptest.Server) *MovieBox {
	m := NewMovieBox()
	// Override baseURLs to use the test server
	m.baseURLs = []string{srv.URL}
	m.client = srv.Client()
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestMovieBoxSearch(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	results, err := m.Search("hail")
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].ID != "1857349212451623008" {
		t.Errorf("unexpected ID: %s", results[0].ID)
	}
	if results[0].Title != "Project Hail Mary" {
		t.Errorf("unexpected title: %s", results[0].Title)
	}
	if results[0].Type != media.Movie {
		t.Errorf("expected Movie type, got %v", results[0].Type)
	}
	if results[0].Year != "2025" {
		t.Errorf("expected year 2025, got %s", results[0].Year)
	}

	if results[1].Type != media.TV {
		t.Errorf("expected TV type for Breaking Bad, got %v", results[1].Type)
	}
	if results[1].Seasons != 5 {
		t.Errorf("expected 5 seasons, got %d", results[1].Seasons)
	}
}

func TestMovieBoxGetDetails(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	detail, err := m.GetDetails("1857349212451623008")
	if err != nil {
		t.Fatalf("GetDetails returned error: %v", err)
	}

	if detail.Description != "A lone astronaut must save the earth." {
		t.Errorf("unexpected description: %s", detail.Description)
	}
	if detail.Rating != "8.5" {
		t.Errorf("expected rating 8.5, got %s", detail.Rating)
	}
	if detail.Duration != "124 min" {
		t.Errorf("expected duration '124 min', got %s", detail.Duration)
	}
	if len(detail.Genre) != 2 {
		t.Errorf("expected 2 genres, got %d", len(detail.Genre))
	}
}

func TestMovieBoxGetSeasons(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	seasons, err := m.GetSeasons("2857349212451623009")
	if err != nil {
		t.Fatalf("GetSeasons returned error: %v", err)
	}

	if len(seasons) != 2 {
		t.Fatalf("expected 2 seasons, got %d", len(seasons))
	}

	if seasons[0].Number != 1 || seasons[0].ID != "1001" {
		t.Errorf("unexpected season 0: %+v", seasons[0])
	}
	if seasons[1].Number != 2 || seasons[1].ID != "1002" {
		t.Errorf("unexpected season 1: %+v", seasons[1])
	}
}

func TestMovieBoxGetServers(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	servers, err := m.GetServers("1857349212451623008", "1.5")
	if err != nil {
		t.Fatalf("GetServers returned error: %v", err)
	}

	if len(servers) != 1 {
		t.Fatalf("expected 1 synthetic server, got %d", len(servers))
	}
	if servers[0].Name != "MovieBox" || servers[0].ID != "default" {
		t.Errorf("unexpected server: %+v", servers[0])
	}
}

func TestMovieBoxWatch(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	stream, err := m.Watch("1857349212451623008", "1.5", "default", "720")
	if err != nil {
		t.Fatalf("Watch returned error: %v", err)
	}

	if stream.URL != "https://macdn.aoneroom.com/stream/index.m3u8" {
		t.Errorf("expected HLS URL, got %s", stream.URL)
	}
	if stream.Quality != "1080p" {
		t.Errorf("expected quality 1080p (best available), got %s", stream.Quality)
	}
}

func TestMovieBoxWatch_GeoRestricted(t *testing.T) {
	m := NewMovieBox()
	m.baseURLs = []string{"https://localhost:9999"} // Invalid server

	_, err := m.Watch("1857349212451623008", "", "", "")
	if err == nil {
		t.Fatal("expected error from Watch with invalid server")
	}
}

func TestMovieBoxGetEmbedURL_NotSupported(t *testing.T) {
	m := NewMovieBox()
	_, err := m.GetEmbedURL("someID")
	if err == nil {
		t.Fatal("expected error from GetEmbedURL")
	}
}

func TestMovieBoxImplementsStreamProvider(t *testing.T) {
	var _ StreamProvider = (*MovieBox)(nil)
}

func TestMovieBoxMediaTypeFromSubjectType(t *testing.T) {
	tests := []struct {
		subjectType int
		expected   media.MediaType
	}{
		{1, media.Movie},
		{2, media.TV},
		{3, media.TV},
		{99, media.Movie}, // unknown defaults to Movie
	}

	for _, tt := range tests {
		got := mediaTypeFromSubjectType(tt.subjectType)
		if got != tt.expected {
			t.Errorf("subjectType %d: expected %v, got %v", tt.subjectType, tt.expected, got)
		}
	}
}

func TestStreamQualityRank(t *testing.T) {
	tests := []struct {
		quality string
		rank    int
	}{
		{"4k", 5},
		{"1080p", 4},
		{"720p", 3},
		{"480p", 2},
		{"360p", 1},
		{"unknown", 0},
	}

	for _, tt := range tests {
		got := streamQualityRank(tt.quality)
		if got != tt.rank {
			t.Errorf("quality %s: expected rank %d, got %d", tt.quality, tt.rank, got)
		}
	}
}

func TestMovieBoxTrending(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	results, err := m.Trending(media.Movie)
	if err != nil {
		t.Fatalf("Trending returned error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results from tab endpoint, got %d", len(results))
	}
}

func TestReverseString(t *testing.T) {
	tests := []struct {
		input   string
		reverse string
	}{
		{"123", "321"},
		{"hello", "olleh"},
		{"", ""},
		{"a", "a"},
	}

	for _, tt := range tests {
		got := reverseString(tt.input)
		if got != tt.reverse {
			t.Errorf("reverse(%q) = %q, want %q", tt.input, got, tt.reverse)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------

// ignore pref: keep b.N loop for Go 1.22 compatibility
func BenchmarkMovieBoxSearch(b *testing.B) {
	srv := newMovieBoxTestServer(&testing.T{})
	m := newMovieBoxForTest(srv)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Search("test")
	}
}

// ---------------------------------------------------------------------------
// Test data JSON files for reference
// ---------------------------------------------------------------------------

func TestMovieBoxTestdataValidJSON(t *testing.T) {
	// Verify test data files parse correctly
	var search mbSearchResponse
	if err := json.Unmarshal([]byte(testSearchResponse), &search); err != nil {
		t.Errorf("test search response is not valid JSON: %v", err)
	}

	var detail mbDetailResponse
	if err := json.Unmarshal([]byte(testDetailResponse), &detail); err != nil {
		t.Errorf("test detail response is not valid JSON: %v", err)
	}

	var play mbPlayInfoResponse
	if err := json.Unmarshal([]byte(testPlayInfoResponse), &play); err != nil {
		t.Errorf("test play info response is not valid JSON: %v", err)
	}

	if !play.HasResource || len(play.HLS) == 0 {
		t.Error("test play info should have resources")
	}
}

func TestMovieBoxNoResourceError(t *testing.T) {
	noResourceResponse := `{"hls": [], "streams": [], "hasResource": false}`

	mux := http.NewServeMux()
	mux.HandleFunc("/wefeed-mobile-bff/subject-api/play-info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(noResourceResponse))
	})

	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	m := newMovieBoxForTest(srv)
	_, err := m.Watch("1857349212451623008", "", "", "")

	if err == nil {
		t.Fatal("expected error for no resource")
	}
	if !strings.Contains(err.Error(), "no streams available") {
		t.Errorf("error should mention 'no streams available', got: %v", err)
	}
}