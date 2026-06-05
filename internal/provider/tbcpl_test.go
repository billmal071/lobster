package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lobster/internal/media"
)

func TestNewTBCPL(t *testing.T) {
	p := NewTBCPL("tbcpl")
	if p == nil {
		t.Fatal("NewTBCPL returned nil")
	}
	if p.base != tbcplDefaultBase {
		t.Errorf("expected TBCPL alias to normalize to %q, got %q", tbcplDefaultBase, p.base)
	}

	p = NewTBCPL("1shows.org")
	if p.base != "https://1shows.org" {
		t.Errorf("expected explicit host to get https scheme, got %q", p.base)
	}
}

func TestTBCPLSearchAndDetails(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/trending" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("query"); got != "breaking bad" {
			t.Errorf("query = %q, want breaking bad", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"results": [
				"breaking bad",
				{
					"id": 1396,
					"name": "Breaking Bad",
					"media_type": "tv",
					"overview": "A chemistry teacher starts over.",
					"first_air_date": "2008-01-20",
					"vote_average": 8.9
				},
				{
					"id": 299536,
					"title": "Avengers: Infinity War",
					"media_type": "movie",
					"overview": "The Avengers face Thanos.",
					"release_date": "2018-04-25",
					"vote_average": 8.2
				}
			]
		}`)
	})
	defer cleanup()

	results, err := p.Search("breaking bad")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].ID != "tv/1396" || results[0].Title != "Breaking Bad" || results[0].Type != media.TV || results[0].Year != "2008" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[1].ID != "movie/299536" || results[1].Type != media.Movie || results[1].Year != "2018" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}

	details, err := p.GetDetails("tv/1396")
	if err != nil {
		t.Fatalf("GetDetails error: %v", err)
	}
	if details.Description != "A chemistry teacher starts over." {
		t.Errorf("description = %q", details.Description)
	}
	if details.Rating != "8.9" {
		t.Errorf("rating = %q, want 8.9", details.Rating)
	}
	if details.Released != "2008-01-20" {
		t.Errorf("released = %q, want 2008-01-20", details.Released)
	}
}

func TestTBCPLSeasonsAndEpisodes(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tv/1399/season/1":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"name": "Season 1",
				"season_number": 1,
				"episodes": [
					{"episode_number": 1, "season_number": 1, "name": "Winter Is Coming"},
					{"episode_number": 2, "season_number": 1, "name": "The Kingsroad"}
				]
			}`)
		case "/api/tv/1399/season/2":
			http.Error(w, `{"error":"Forbidden: Not Allowed Gang"}`, http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	})
	defer cleanup()

	seasons, err := p.GetSeasons("tv/1399")
	if err != nil {
		t.Fatalf("GetSeasons error: %v", err)
	}
	if len(seasons) != 1 || seasons[0].Number != 1 || seasons[0].ID != "1399:1" {
		t.Fatalf("unexpected seasons: %+v", seasons)
	}

	episodes, err := p.GetEpisodes("tv/1399", seasons[0].ID)
	if err != nil {
		t.Fatalf("GetEpisodes error: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("len(episodes) = %d, want 2", len(episodes))
	}
	if episodes[1].Number != 2 || episodes[1].Title != "The Kingsroad" || episodes[1].ID != "1399:1:2" {
		t.Fatalf("unexpected episode: %+v", episodes[1])
	}
}

func TestTBCPLWatchMovie(t *testing.T) {
	const streamURL = "https://cdn.example.test/movie/master.m3u8"
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("id"); got != "1122573" {
			t.Errorf("id = %q, want 1122573", got)
		}
		if got := r.URL.Query().Get("sr"); got != "0" {
			t.Errorf("sr = %q, want 0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"url": [
				{"lang": "English", "link": %q, "type": "hls", "name": "Togi"}
			],
			"tracks": [
				{"lang": "English", "url": "https://subs.example.test/en.vtt"}
			]
		}`, encryptTBCPLVidzeeLinkForTest(t, streamURL, "test-key"))
	})
	defer cleanup()
	p.vidzeeKey = "test-key"

	stream, err := p.Watch("movie/1122573", "", "Togi", "1080")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if stream.URL != streamURL {
		t.Errorf("stream.URL = %q, want %q", stream.URL, streamURL)
	}
	if stream.Referer != p.vidzeeBaseURL+"/" {
		t.Errorf("referer = %q", stream.Referer)
	}
	if len(stream.Subtitles) != 1 || !strings.Contains(stream.Subtitles[0].URL, "en.vtt") {
		t.Errorf("unexpected subtitles: %+v", stream.Subtitles)
	}
}

func TestTBCPLWatchTV(t *testing.T) {
	p, _, cleanup := newTestTBCPL(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/server" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("id"); got != "1399" {
			t.Errorf("id = %q, want 1399", got)
		}
		if got := r.URL.Query().Get("ss"); got != "1" {
			t.Errorf("ss = %q, want 1", got)
		}
		if got := r.URL.Query().Get("ep"); got != "2" {
			t.Errorf("ep = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"url":[{"link":%q}]}`, encryptTBCPLVidzeeLinkForTest(t, "https://cdn.example.test/tv/master.m3u8", "test-key"))
	})
	defer cleanup()
	p.vidzeeKey = "test-key"

	stream, err := p.Watch("tv/1399", "1399:1:2", "vidzee:0", "720")
	if err != nil {
		t.Fatalf("Watch error: %v", err)
	}
	if !strings.Contains(stream.URL, "/tv/") {
		t.Errorf("unexpected stream URL: %q", stream.URL)
	}
}

func newTestTBCPL(t *testing.T, handler http.HandlerFunc) (*TBCPL, *httptest.Server, func()) {
	t.Helper()
	ts := httptest.NewServer(handler)
	p := NewTBCPL(ts.URL)
	p.client = ts.Client()
	p.tmdbBaseURL = ts.URL
	p.vidzeeBaseURL = ts.URL
	p.vidzeeKeyURL = ts.URL + "/api-key"
	return p, ts, ts.Close
}

func encryptTBCPLVidzeeLinkForTest(t *testing.T, plain, key string) string {
	t.Helper()

	aesKey := []byte(key)
	if len(aesKey) < 32 {
		padded := make([]byte, 32)
		copy(padded, aesKey)
		aesKey = padded
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	iv := []byte("1234567890abcdef")
	padded := pkcs7PadForTest([]byte(plain), aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	joined := base64.StdEncoding.EncodeToString(iv) + ":" + base64.StdEncoding.EncodeToString(ciphertext)
	return base64.StdEncoding.EncodeToString([]byte(joined))
}

func pkcs7PadForTest(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padLen)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padLen)
	}
	return out
}
