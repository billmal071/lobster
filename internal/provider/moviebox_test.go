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
// Test data (new API format with envelope)
// ---------------------------------------------------------------------------

const testSearchResponseV2 = `{
  "code": 0,
  "message": "ok",
  "data": {
    "results": [
      {
        "topicType": "SUBJECT",
        "subjects": [
          {"subjectId": "1857349212451623008", "subjectType": 1, "title": "Project Hail Mary", "releaseDate": "2025-03-14", "duration": "2h 4m", "genre": "Sci-Fi, Adventure", "hasResource": true, "seNum": 0, "imdbRatingValue": "8.5", "countryName": "USA", "description": "A lone astronaut must save the earth."},
          {"subjectId": "2857349212451623009", "subjectType": 2, "title": "Breaking Bad", "releaseDate": "2008-01-20", "duration": "", "genre": "Drama, Crime", "hasResource": true, "seNum": 5, "imdbRatingValue": "9.5", "countryName": "USA", "description": "A chemistry teacher turns to crime."}
        ]
      }
    ]
  }
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

const testTabResponse = `{
  "code": 0,
  "message": "ok",
  "data": {
    "results": [
      {
        "topicType": "SUBJECT",
        "subjects": [
          {"subjectId": "100", "subjectType": 1, "title": "Trending Movie", "releaseDate": "2025-01-01", "duration": "1h 30m", "genre": "Action", "hasResource": true, "seNum": 0}
        ]
      }
    ]
  }
}`

// ---------------------------------------------------------------------------
// Test server
// ---------------------------------------------------------------------------

func newMovieBoxTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/wefeed-mobile-bff/tab-operating", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testTabResponse))
	})

	mux.HandleFunc("/wefeed-mobile-bff/subject-api/search/v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(testSearchResponseV2))
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

	// Search first to populate cache.
	_, err := m.Search("hail")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

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
	if detail.Duration != "2h 4m" {
		t.Errorf("expected duration '2h 4m', got %s", detail.Duration)
	}
	if len(detail.Genre) != 2 {
		t.Errorf("expected 2 genres, got %d", len(detail.Genre))
	}
}

func TestMovieBoxGetSeasons(t *testing.T) {
	srv := newMovieBoxTestServer(t)
	m := newMovieBoxForTest(srv)

	// Search first to populate cache.
	_, err := m.Search("hail")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	seasons, err := m.GetSeasons("2857349212451623009")
	if err != nil {
		t.Fatalf("GetSeasons returned error: %v", err)
	}

	if len(seasons) != 5 {
		t.Fatalf("expected 5 seasons (from seNum), got %d", len(seasons))
	}
	if seasons[0].Number != 1 || seasons[0].ID != "1" {
		t.Errorf("unexpected season 0: %+v", seasons[0])
	}
}

func TestMovieBoxGetEpisodes(t *testing.T) {
	m := NewMovieBox()

	episodes, err := m.GetEpisodes("123", "2")
	if err != nil {
		t.Fatalf("GetEpisodes returned error: %v", err)
	}

	if len(episodes) != 10 {
		t.Fatalf("expected 10 episodes, got %d", len(episodes))
	}
	if episodes[0].ID != "2.1" {
		t.Errorf("expected episode ID '2.1', got %s", episodes[0].ID)
	}
}

func TestMovieBoxGetServers(t *testing.T) {
	m := NewMovieBox()

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
		expected    media.MediaType
	}{
		{1, media.Movie},
		{2, media.TV},
		{3, media.TV},
		{99, media.Movie},
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

	if len(results) != 1 {
		t.Fatalf("expected 1 result from tab endpoint, got %d", len(results))
	}
	if results[0].Title != "Trending Movie" {
		t.Errorf("unexpected title: %s", results[0].Title)
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

func TestMovieBoxTestdataValidJSON(t *testing.T) {
	var envelope mbAPIResponse
	if err := json.Unmarshal([]byte(testSearchResponseV2), &envelope); err != nil {
		t.Errorf("test search response is not valid JSON: %v", err)
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
