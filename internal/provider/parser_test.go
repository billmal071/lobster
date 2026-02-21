package provider

import (
	"os"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/media"
)

func loadTestDoc(t *testing.T, filename string) *goquery.Document {
	t.Helper()
	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatalf("reading test fixture %s: %v", filename, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("parsing test fixture %s: %v", filename, err)
	}
	return doc
}

func TestParseSearchResults(t *testing.T) {
	doc := loadTestDoc(t, "search_results.html")
	results := parseSearchResults(doc)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First result: movie
	if results[0].Title != "The Exorcist" {
		t.Errorf("result[0].Title = %q, want 'The Exorcist'", results[0].Title)
	}
	if results[0].Type != media.Movie {
		t.Errorf("result[0].Type = %v, want Movie", results[0].Type)
	}
	if results[0].Year != "1973" {
		t.Errorf("result[0].Year = %q, want '1973'", results[0].Year)
	}
	if results[0].ID != "movie/free-the-exorcist-hd-75043" {
		t.Errorf("result[0].ID = %q, want 'movie/free-the-exorcist-hd-75043'", results[0].ID)
	}

	// Second result: TV show
	if results[1].Title != "Breaking Bad" {
		t.Errorf("result[1].Title = %q, want 'Breaking Bad'", results[1].Title)
	}
	if results[1].Type != media.TV {
		t.Errorf("result[1].Type = %v, want TV", results[1].Type)
	}
}

func TestParseSearchResultsMalicious(t *testing.T) {
	doc := loadTestDoc(t, "search_malicious.html")
	results := parseSearchResults(doc)

	// Malicious titles should be parsed as plain text — no code execution
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results from malicious HTML, got %d", len(results))
	}

	// Shell injection attempt should be plain text
	if results[0].Title != "'; rm -rf / #" {
		t.Errorf("shell injection title = %q, want literal string", results[0].Title)
	}

	// Command substitution should be plain text
	if results[1].Title != "$(whoami)" {
		t.Errorf("command substitution title = %q, want literal string", results[1].Title)
	}
}

func TestExtractID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/movie/free-the-exorcist-hd-75043", "movie/free-the-exorcist-hd-75043"},
		{"/tv/watch-breaking-bad-39516", "tv/watch-breaking-bad-39516"},
		{"/movie/test-123?ref=home", "movie/test-123"},
		{"movie/test", "movie/test"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractID(tt.input)
			if got != tt.expected {
				t.Errorf("extractID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractNumericID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"movie/free-the-exorcist-hd-75043", "75043"},
		{"tv/watch-breaking-bad-39516", "39516"},
		{"no-number-here", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractNumericID(tt.input)
			if got != tt.expected {
				t.Errorf("extractNumericID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseTrendingResultsMovies(t *testing.T) {
	doc := loadTestDoc(t, "home_trending.html")
	results := parseTrendingResults(doc, media.Movie)

	if len(results) != 2 {
		t.Fatalf("expected 2 trending movies, got %d", len(results))
	}

	if results[0].Title != "Dune: Part Two" {
		t.Errorf("result[0].Title = %q, want 'Dune: Part Two'", results[0].Title)
	}
	if results[0].Type != media.Movie {
		t.Errorf("result[0].Type = %v, want Movie", results[0].Type)
	}
	if results[0].Year != "2024" {
		t.Errorf("result[0].Year = %q, want '2024'", results[0].Year)
	}
	if results[0].ID != "movie/free-dune-part-two-hd-98765" {
		t.Errorf("result[0].ID = %q, want 'movie/free-dune-part-two-hd-98765'", results[0].ID)
	}

	if results[1].Title != "Oppenheimer" {
		t.Errorf("result[1].Title = %q, want 'Oppenheimer'", results[1].Title)
	}
}

func TestParseTrendingResultsTV(t *testing.T) {
	doc := loadTestDoc(t, "home_trending.html")
	results := parseTrendingResults(doc, media.TV)

	if len(results) != 2 {
		t.Fatalf("expected 2 trending TV shows, got %d", len(results))
	}

	if results[0].Title != "The Last of Us" {
		t.Errorf("result[0].Title = %q, want 'The Last of Us'", results[0].Title)
	}
	if results[0].Type != media.TV {
		t.Errorf("result[0].Type = %v, want TV", results[0].Type)
	}
	if results[0].Year != "2023" {
		t.Errorf("result[0].Year = %q, want '2023'", results[0].Year)
	}

	if results[1].Title != "Shogun" {
		t.Errorf("result[1].Title = %q, want 'Shogun'", results[1].Title)
	}
	if results[1].Year != "2024" {
		t.Errorf("result[1].Year = %q, want '2024'", results[1].Year)
	}
}

func TestParseTrendingResultsEmpty(t *testing.T) {
	doc := loadTestDoc(t, "search_results.html")
	// search_results.html has no #trending-movies or #trending-tv panels
	results := parseTrendingResults(doc, media.Movie)

	if len(results) != 0 {
		t.Errorf("expected 0 results for missing panel, got %d", len(results))
	}
}

func TestFormatDisplayTitle(t *testing.T) {
	tests := []struct {
		name     string
		result   media.SearchResult
		expected string
	}{
		{
			"movie with year",
			media.SearchResult{Title: "Inception", Year: "2010", Type: media.Movie},
			"Inception (2010) [Movie]",
		},
		{
			"tv without year",
			media.SearchResult{Title: "Breaking Bad", Type: media.TV},
			"Breaking Bad [TV]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDisplayTitle(tt.result)
			if got != tt.expected {
				t.Errorf("FormatDisplayTitle() = %q, want %q", got, tt.expected)
			}
		})
	}
}
