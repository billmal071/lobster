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
	tmdbSearchBase             = "https://www.themoviedb.org"
	moviesapiBase              = "https://ww2.moviesapi.to"
	moviesapiPlayerKey         = "kiemtienmua911ca"
	moviesapiPlayerIV          = "1234567890oiuytr"
	soap2dayUserAgent          = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0"
	soap2dayStreamProbeSize    = 4096
	moviesapiPlaceholderPlayer = "player.mov2day.xyz"
)

// Soap2Day implements the StreamProvider interface using TMDB for search
// and moviesapi.to player APIs for stream resolution.
type Soap2Day struct {
	client           *http.Client
	tmdbBaseURL      string
	moviesapiBaseURL string
}

// NewSoap2Day creates a new Soap2Day provider.
func NewSoap2Day() *Soap2Day {
	return &Soap2Day{
		client:           httputil.NewClient(),
		tmdbBaseURL:      tmdbSearchBase,
		moviesapiBaseURL: moviesapiBase,
	}
}

// --- TMDB search types ---

type tmdbSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tmdbSearchResult struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`      // movie
	Name         string  `json:"name"`       // tv
	MediaType    string  `json:"media_type"` // "movie" or "tv"
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`   // movie
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

// hd4uDecrypted represents the decrypted response from hd4u.sbs,
// which includes the stream source and optional embedded subtitles.
type hd4uDecrypted struct {
	Source   string            `json:"source"`
	Subtitle map[string]string `json:"subtitle"`
}

type moviesapiSubtitle struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type moviesapiPlayerDecrypted struct {
	Source          string            `json:"source"`
	CF              string            `json:"cf"`
	HLSVideoGoogle  string            `json:"hlsVideoGoogle"`
	HLSVideoTiktok  string            `json:"hlsVideoTiktok"`
	HLSVideoTwitter string            `json:"hlsVideoTwitter"`
	Subtitle        map[string]string `json:"subtitle"`
}

// --- Provider interface ---

// Search uses TMDB's free trending search endpoint (no API key needed).
func (s *Soap2Day) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search/trending?query=%s",
		s.tmdbBaseURL, url.QueryEscape(query))

	body, err := s.fetchWithReferer(searchURL, s.tmdbBaseURL+"/")
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
			ID:        fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:     item.displayTitle(),
			Type:      mt,
			Year:      item.year(),
			URL:       fmt.Sprintf("%s/%s/%d", s.tmdbBaseURL, item.MediaType, item.ID),
			PosterURL: tmdbPosterURL(item.PosterPath),
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
		apiURL := fmt.Sprintf("%s/api/tv/%s/%d/1", s.moviesapiBaseURL, tmdbID, n)
		body, err := s.fetchWithReferer(apiURL, s.moviesapiBaseURL+"/")
		if err != nil {
			break
		}
		var resp moviesapiResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			break
		}
if !hasSupportedMoviesAPIPlayer(resp) {
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
		apiURL := fmt.Sprintf("%s/api/tv/%s/%d/%d", s.moviesapiBaseURL, tmdbID, seasonNum, ep)
		body, err := s.fetchWithReferer(apiURL, s.moviesapiBaseURL+"/")
		if err != nil {
			break
		}
		var resp moviesapiResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			break
		}
if !hasSupportedMoviesAPIPlayer(resp) {
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

// Watch resolves the stream URL through moviesapi.to player decryption.
func (s *Soap2Day) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	// Build moviesapi URL from the encoded IDs
	var apiURL string
	if episodeID != "" {
		// TV: episodeID format is "tmdbID:season:episode"
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		apiURL = fmt.Sprintf("%s/api/tv/%s/%s/%s", s.moviesapiBaseURL, parts[0], parts[1], parts[2])
	} else {
		// Movie: mediaID format is "movie/{tmdbID}"
		tmdbID := extractTMDBID(mediaID)
		apiURL = fmt.Sprintf("%s/api/movie/%s", s.moviesapiBaseURL, tmdbID)
	}

// Fetch moviesapi response
	apiBody, err := s.fetchWithReferer(apiURL, s.moviesapiBaseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("moviesapi request: %w", err)
	}

	var apiResp moviesapiResponse
	if err := json.Unmarshal(apiBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing moviesapi response: %w", err)
	}

	stream, err := s.resolveMoviesAPIStream(apiResp, quality)
	if err != nil {
		return nil, err
	}

	return stream, nil
}

// extractTMDBID extracts the numeric TMDB ID from a provider ID like "tv/79744" or "movie/299534".
func extractTMDBID(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

type moviesapiPlayerRef struct {
	rawURL string
	base   string
	embed  string
}

func hasSupportedMoviesAPIPlayer(resp moviesapiResponse) bool {
	for _, raw := range []string{resp.VideoURL, resp.UpnURL} {
		ref, err := parseMoviesAPIPlayerRef(raw)
		if err == nil && ref.embed != "" {
			return true
		}
	}
	return false
}

func parseMoviesAPIPlayerRef(rawURL string) (moviesapiPlayerRef, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return moviesapiPlayerRef{}, fmt.Errorf("empty player URL")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return moviesapiPlayerRef{}, fmt.Errorf("parsing player URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return moviesapiPlayerRef{}, fmt.Errorf("invalid player URL: %s", rawURL)
	}
	if strings.Contains(parsed.Host, moviesapiPlaceholderPlayer) {
		return moviesapiPlayerRef{}, fmt.Errorf("placeholder player URL: %s", rawURL)
	}
	if !isSupportedMoviesAPIPlayerHost(parsed.Host) && !isLocalMoviesAPIPlayer(parsed.Host) {
		return moviesapiPlayerRef{}, fmt.Errorf("unsupported player host: %s", parsed.Host)
	}

	fragment := parsed.Fragment
	if ampIdx := strings.Index(fragment, "&"); ampIdx >= 0 {
		fragment = fragment[:ampIdx]
	}
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return moviesapiPlayerRef{}, fmt.Errorf("empty embed ID in player URL")
	}

	base := (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
	return moviesapiPlayerRef{rawURL: rawURL, base: base, embed: fragment}, nil
}

func isSupportedMoviesAPIPlayerHost(host string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	return host == "hd4u.sbs" || host == "flixcdn.cyou"
}

func isLocalMoviesAPIPlayer(host string) bool {
	host = strings.ToLower(host)
	return strings.HasPrefix(host, "127.0.0.1:") ||
		strings.HasPrefix(host, "localhost:") ||
		strings.HasPrefix(host, "[::1]:")
}

func (s *Soap2Day) resolveMoviesAPIStream(apiResp moviesapiResponse, quality string) (*media.Stream, error) {
	refs := moviesapiPlayerRefs(apiResp)
	if len(refs) == 0 {
		return nil, fmt.Errorf("no supported player URLs in moviesapi response")
	}

	var lastErr error
	for _, ref := range refs {
		decrypted, err := s.decryptMoviesAPIPlayer(ref)
		if err != nil {
			lastErr = err
			continue
		}

		for _, streamURL := range decrypted.streamCandidates() {
			if err := s.validateMoviesAPIStreamURL(streamURL, ref.base); err != nil {
				lastErr = err
				continue
			}

			return &media.Stream{
				URL:       streamURL,
				Quality:   quality,
				Subtitles: mergeMoviesAPISubtitles(apiResp.Subtitles, decrypted.Subtitle, ref.base),
				Referer:   strings.TrimRight(ref.base, "/") + "/",
				UserAgent: soap2dayUserAgent,
			}, nil
		}

		lastErr = fmt.Errorf("player %s returned no stream URLs", ref.base)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no stream URLs found")
	}
	return nil, fmt.Errorf("stream decryption: %w", lastErr)
}

func moviesapiPlayerRefs(apiResp moviesapiResponse) []moviesapiPlayerRef {
	var refs []moviesapiPlayerRef
	seen := make(map[string]bool)
	for _, raw := range []string{apiResp.VideoURL, apiResp.UpnURL} {
		ref, err := parseMoviesAPIPlayerRef(raw)
		if err != nil {
			continue
		}
		key := ref.base + "#" + ref.embed
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	return refs
}

func (d moviesapiPlayerDecrypted) streamCandidates() []string {
	var candidates []string
	seen := make(map[string]bool)
	for _, candidate := range []string{
		d.Source,
		d.CF,
		d.HLSVideoGoogle,
		d.HLSVideoTiktok,
		d.HLSVideoTwitter,
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	return candidates
}

// decryptMoviesAPIPlayer fetches encrypted stream data from a moviesapi player and decrypts it.
func (s *Soap2Day) decryptMoviesAPIPlayer(ref moviesapiPlayerRef) (moviesapiPlayerDecrypted, error) {
	apiURL := fmt.Sprintf("%s/api/v1/video?id=%s&w=1920&h=1080&r=", ref.base, url.QueryEscape(ref.embed))

	body, err := s.fetchWithReferer(apiURL, strings.TrimRight(ref.base, "/")+"/")
	if err != nil {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("player request: %w", err)
	}

	// Check for JSON error responses (e.g. {"message": "Video not found or deleted"})
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var errResp struct{ Message string `json:"message"` }
		if json.Unmarshal([]byte(trimmed), &errResp) == nil && errResp.Message != "" {
			return moviesapiPlayerDecrypted{}, fmt.Errorf("%s", errResp.Message)
		}
	}

	// Response is hex-encoded AES-CBC encrypted data
	ciphertext, err := hex.DecodeString(trimmed)
	if err != nil {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("hex decode: %w", err)
	}

	// Decrypt AES-CBC
	block, err := aes.NewCipher([]byte(moviesapiPlayerKey))
	if err != nil {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("aes cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}

	mode := cipher.NewCBCDecrypter(block, []byte(moviesapiPlayerIV))
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// PKCS7 unpad
	if len(plaintext) == 0 {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("empty decrypted response")
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > 0 && padLen <= aes.BlockSize && padLen <= len(plaintext) {
		plaintext = plaintext[:len(plaintext)-padLen]
	}

var decrypted moviesapiPlayerDecrypted
	if err := json.Unmarshal(plaintext, &decrypted); err != nil {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("parsing decrypted response: %w", err)
	}

	if len(decrypted.streamCandidates()) == 0 {
		return moviesapiPlayerDecrypted{}, fmt.Errorf("empty source in decrypted response")
	}

	return decrypted, nil
}

func (s *Soap2Day) validateMoviesAPIStreamURL(streamURL, referer string) error {
	req, err := http.NewRequest(http.MethodGet, streamURL, nil)
	if err != nil {
		return fmt.Errorf("creating stream probe: %w", err)
	}
	req.Header.Set("User-Agent", soap2dayUserAgent)
	req.Header.Set("Accept", "application/vnd.apple.mpegurl, application/x-mpegURL, video/*, */*")
	req.Header.Set("Referer", strings.TrimRight(referer, "/")+"/")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("probing stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("probing stream: status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, soap2dayStreamProbeSize))
	if err != nil {
		return fmt.Errorf("reading stream probe: %w", err)
	}
	probe := strings.TrimSpace(string(body))
	if strings.HasPrefix(probe, "#EXTM3U") ||
		strings.Contains(contentType, "mpegurl") ||
		strings.Contains(contentType, "application/vnd.apple") ||
		strings.Contains(contentType, "video/") {
		return nil
	}

	return fmt.Errorf("probing stream: non-media response")
}

func mergeMoviesAPISubtitles(apiSubs []moviesapiSubtitle, playerSubs map[string]string, playerBase string) []media.Subtitle {
	var subtitles []media.Subtitle
	seen := make(map[string]bool)

	appendSub := func(label, rawURL string) {
		label = strings.TrimSpace(label)
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" {
			return
		}
		subURL := resolveMediaURL(playerBase, rawURL)
		key := strings.ToLower(label) + "|" + subURL
		if seen[key] {
			return
		}
		seen[key] = true
		subtitles = append(subtitles, media.Subtitle{
			Language: label,
			Label:    label,
			URL:      subURL,
		})
	}

	for _, sub := range apiSubs {
		appendSub(sub.Label, sub.URL)
	}
	for label, subURL := range playerSubs {
		appendSub(label, subURL)
	}

	return subtitles
}

// fetchWithReferer performs a GET request with a specific Referer header.
func (s *Soap2Day) fetchWithReferer(rawURL, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", soap2dayUserAgent)
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

// Trending returns trending content from TMDB.
func (s *Soap2Day) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	return s.fetchTMDBTrending(mt)
}

// Recent returns recently added content (uses trending as proxy).
func (s *Soap2Day) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return s.Trending(mediaType)
}

func (s *Soap2Day) fetchTMDBTrending(mediaType string) ([]media.SearchResult, error) {
	// TMDB's /search/trending endpoint returns trending results with a minimal query.
	trendingURL := fmt.Sprintf("%s/search/trending?query=a", s.tmdbBaseURL)

	body, err := s.fetchWithReferer(trendingURL, s.tmdbBaseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("trending: %w", err)
	}

	var resp tmdbSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("trending: parsing response: %w", err)
	}

	var results []media.SearchResult
	for _, raw := range resp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}

		var item tmdbSearchResult
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		if item.MediaType == "" {
			item.MediaType = mediaType
		}

		// Filter by requested media type.
		if item.MediaType != mediaType {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}

		results = append(results, media.SearchResult{
			ID:        fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:     item.displayTitle(),
			Type:      mt,
			Year:      item.year(),
			URL:       fmt.Sprintf("%s/%s/%d", s.tmdbBaseURL, item.MediaType, item.ID),
			PosterURL: tmdbPosterURL(item.PosterPath),
		})
	}

	return results, nil
}
