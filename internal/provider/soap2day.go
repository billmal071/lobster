package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	tmdbSearchBase = "https://www.themoviedb.org"
	moviesapiBase  = "https://ww2.moviesapi.to"
	flixcdnBase    = "https://flixcdn.cyou"
	flixcdnKey     = "kiemtienmua911ca"
	flixcdnIV      = "1234567890oiuytr"
)

// Soap2Day implements the StreamProvider interface using TMDB for search
// and moviesapi.to + flixcdn for stream resolution.
type Soap2Day struct {
	client *http.Client
}

// NewSoap2Day creates a new Soap2Day provider.
func NewSoap2Day() *Soap2Day {
	return &Soap2Day{
		client: httputil.NewClient(),
	}
}

// --- TMDB search types ---

type tmdbSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tmdbSearchResult struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`        // movie
	Name         string  `json:"name"`         // tv
	MediaType    string  `json:"media_type"`   // "movie" or "tv"
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"` // movie
	FirstAirDate string  `json:"first_air_date"` // tv
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
}

func (r *tmdbSearchResult) displayTitle() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Title
}

func (r *tmdbSearchResult) year() string {
	date := r.ReleaseDate
	if date == "" {
		date = r.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// --- moviesapi types ---

type moviesapiResponse struct {
	VideoURL  string              `json:"video_url"`
	UpnURL    string              `json:"upn_url"`
	Subtitles []moviesapiSubtitle `json:"subtitles"`
}

type moviesapiSubtitle struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type flixcdnDecrypted struct {
	Source string `json:"source"`
}

// --- Provider interface ---

// Search uses TMDB's free trending search endpoint (no API key needed).
func (s *Soap2Day) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search/trending?query=%s",
		tmdbSearchBase, url.QueryEscape(query))

	body, err := s.fetchWithReferer(searchURL, tmdbSearchBase+"/")
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	var resp tmdbSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("search: parsing response: %w", err)
	}

	var results []media.SearchResult
	for _, raw := range resp.Results {
		// Skip non-object entries (TMDB includes suggestion strings in the array)
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}

		var item tmdbSearchResult
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		if item.MediaType != "movie" && item.MediaType != "tv" {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}

		results = append(results, media.SearchResult{
			ID:    fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title: item.displayTitle(),
			Type:  mt,
			Year:  item.year(),
			URL:   fmt.Sprintf("%s/%s/%d", tmdbSearchBase, item.MediaType, item.ID),
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}

	return results, nil
}

// GetDetails returns metadata from TMDB search results (minimal).
func (s *Soap2Day) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

// GetSeasons returns seasons for a TV show by probing moviesapi.to.
// It tests season numbers starting from 1 until one returns no real content.
func (s *Soap2Day) GetSeasons(id string) ([]media.Season, error) {
	tmdbID := extractTMDBID(id)

	var seasons []media.Season
	for n := 1; n <= 30; n++ {
		apiURL := fmt.Sprintf("%s/api/tv/%s/%d/1", moviesapiBase, tmdbID, n)
		body, err := s.fetchWithReferer(apiURL, moviesapiBase+"/")
		if err != nil {
			break
		}
		var resp moviesapiResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			break
		}
		// Real content has flixcdn.cyou in the video_url; fallback URLs like
		// player.mov2day.xyz indicate the content doesn't actually exist.
		if !strings.Contains(resp.VideoURL, "flixcdn.cyou") {
			break
		}
		seasons = append(seasons, media.Season{
			Number: n,
			ID:     fmt.Sprintf("%s:%d", tmdbID, n),
		})
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found")
	}

	return seasons, nil
}

// GetEpisodes returns episodes for a season by probing moviesapi.to.
func (s *Soap2Day) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	parts := strings.SplitN(seasonID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid season ID: %s", seasonID)
	}
	tmdbID := parts[0]
	seasonNum, _ := strconv.Atoi(parts[1])

	var episodes []media.Episode
	for ep := 1; ep <= 50; ep++ {
		apiURL := fmt.Sprintf("%s/api/tv/%s/%d/%d", moviesapiBase, tmdbID, seasonNum, ep)
		body, err := s.fetchWithReferer(apiURL, moviesapiBase+"/")
		if err != nil {
			break
		}
		var resp moviesapiResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			break
		}
		if !strings.Contains(resp.VideoURL, "flixcdn.cyou") {
			break
		}
		episodes = append(episodes, media.Episode{
			Number: ep,
			Title:  fmt.Sprintf("Episode %d", ep),
			ID:     fmt.Sprintf("%s:%d:%d", tmdbID, seasonNum, ep),
		})
	}

	if len(episodes) == 0 {
		return nil, fmt.Errorf("no episodes found")
	}

	return episodes, nil
}

// GetServers returns a single server for stream resolution.
func (s *Soap2Day) GetServers(id string, episodeID string) ([]media.Server, error) {
	return []media.Server{
		{Name: "Default", ID: "default"},
	}, nil
}

// GetEmbedURL is not used for this provider.
func (s *Soap2Day) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}

// Watch resolves the stream URL through moviesapi.to and flixcdn decryption.
func (s *Soap2Day) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	// Build moviesapi URL from the encoded IDs
	var apiURL string
	if episodeID != "" {
		// TV: episodeID format is "tmdbID:season:episode"
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		apiURL = fmt.Sprintf("%s/api/tv/%s/%s/%s", moviesapiBase, parts[0], parts[1], parts[2])
	} else {
		// Movie: mediaID format is "movie/{tmdbID}"
		tmdbID := extractTMDBID(mediaID)
		apiURL = fmt.Sprintf("%s/api/movie/%s", moviesapiBase, tmdbID)
	}

	// Fetch moviesapi response
	apiBody, err := s.fetchWithReferer(apiURL, moviesapiBase+"/")
	if err != nil {
		return nil, fmt.Errorf("moviesapi request: %w", err)
	}

	var apiResp moviesapiResponse
	if err := json.Unmarshal(apiBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing moviesapi response: %w", err)
	}

	if apiResp.VideoURL == "" {
		return nil, fmt.Errorf("no video_url in response")
	}

	// Extract embed ID from video_url (after # and before first &)
	embedID, err := extractEmbedID(apiResp.VideoURL)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	// Decrypt stream URL from flixcdn
	m3u8URL, err := s.decryptFlixcdn(embedID)
	if err != nil {
		return nil, fmt.Errorf("stream decryption: %w", err)
	}

	// Map subtitles
	var subtitles []media.Subtitle
	for _, sub := range apiResp.Subtitles {
		subtitles = append(subtitles, media.Subtitle{
			Language: sub.Label,
			Label:    sub.Label,
			URL:      sub.URL,
		})
	}

	return &media.Stream{
		URL:       m3u8URL,
		Quality:   quality,
		Subtitles: subtitles,
		Referer:   flixcdnBase + "/",
	}, nil
}

// extractTMDBID extracts the numeric TMDB ID from a provider ID like "tv/79744" or "movie/299534".
func extractTMDBID(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

// extractEmbedID extracts the embed ID from a flixcdn video_url.
func extractEmbedID(videoURL string) (string, error) {
	idx := strings.Index(videoURL, "#")
	if idx < 0 {
		return "", fmt.Errorf("no embed ID in video URL")
	}
	fragment := videoURL[idx+1:]
	if ampIdx := strings.Index(fragment, "&"); ampIdx >= 0 {
		fragment = fragment[:ampIdx]
	}
	if fragment == "" {
		return "", fmt.Errorf("empty embed ID in video URL")
	}
	return fragment, nil
}

// decryptFlixcdn fetches encrypted stream data from flixcdn and decrypts it.
func (s *Soap2Day) decryptFlixcdn(embedID string) (string, error) {
	apiURL := fmt.Sprintf("%s/api/v1/video?id=%s", flixcdnBase, url.QueryEscape(embedID))

	body, err := s.fetchWithReferer(apiURL, flixcdnBase+"/")
	if err != nil {
		return "", fmt.Errorf("flixcdn request: %w", err)
	}

	// Response is hex-encoded AES-CBC encrypted data
	ciphertext, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return "", fmt.Errorf("hex decode: %w", err)
	}

	// Decrypt AES-CBC
	block, err := aes.NewCipher([]byte(flixcdnKey))
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}

	mode := cipher.NewCBCDecrypter(block, []byte(flixcdnIV))
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// PKCS7 unpad
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > 0 && padLen <= aes.BlockSize {
		plaintext = plaintext[:len(plaintext)-padLen]
	}

	var decrypted flixcdnDecrypted
	if err := json.Unmarshal(plaintext, &decrypted); err != nil {
		return "", fmt.Errorf("parsing decrypted response: %w", err)
	}

	if decrypted.Source == "" {
		return "", fmt.Errorf("empty source in decrypted response")
	}

	return decrypted.Source, nil
}

// fetchWithReferer performs a GET request with a specific Referer header.
func (s *Soap2Day) fetchWithReferer(rawURL, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", referer)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

// Trending is not supported.
func (s *Soap2Day) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("trending not supported by soap2day provider")
}

// Recent is not supported.
func (s *Soap2Day) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("recent not supported by soap2day provider")
}
