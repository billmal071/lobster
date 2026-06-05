package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	tbcplDefaultBase     = "https://www.1shows.org"
	tbcplTMDBBase        = "https://www.themoviedb.org"
	tbcplVidzeeBase      = "https://player.vidzee.wtf"
	tbcplVidzeeKeyURL    = "https://core.vidzee.wtf/api-key"
	tbcplVidzeeKeySecret = "c4a8f1d7e2b9a6c3d0f5e8a1b7c4d9e2"
	tbcplMaxSeasons      = 30
)

// TBCPL bridges the TBCPL index to the currently working 1Shows catalog and
// Vidzee stream API. TBCPL itself is an index of sources, not a direct media API.
type TBCPL struct {
	base          string
	client        *http.Client
	tmdbBaseURL   string
	vidzeeBaseURL string
	vidzeeKeyURL  string

	mu          sync.RWMutex
	searchCache map[string]tbcplCatalogItem
	seasonCache map[string]*tbcplSeasonResponse
	vidzeeKey   string
}

// NewTBCPL creates a new TBCPL provider.
func NewTBCPL(base string) *TBCPL {
	return &TBCPL{
		base:          normalizeTBCPLBase(base),
		client:        &http.Client{Timeout: 15 * time.Second, Transport: httputil.NewClient().Transport},
		tmdbBaseURL:   tbcplTMDBBase,
		vidzeeBaseURL: tbcplVidzeeBase,
		vidzeeKeyURL:  tbcplVidzeeKeyURL,
		searchCache:   make(map[string]tbcplCatalogItem),
		seasonCache:   make(map[string]*tbcplSeasonResponse),
	}
}

func normalizeTBCPLBase(base string) string {
	base = strings.TrimSpace(strings.TrimRight(base, "/"))
	lower := strings.ToLower(base)
	if base == "" || lower == "tbcpl" || strings.Contains(lower, "tbcpl") {
		return tbcplDefaultBase
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func (t *TBCPL) baseURL() string {
	return strings.TrimRight(t.base, "/")
}

type tbcplSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tbcplCatalogItem struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	MediaType    string  `json:"media_type"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
}

func (i tbcplCatalogItem) displayTitle() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Title
}

func (i tbcplCatalogItem) year() string {
	date := i.ReleaseDate
	if date == "" {
		date = i.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func (i tbcplCatalogItem) released() string {
	if i.ReleaseDate != "" {
		return i.ReleaseDate
	}
	return i.FirstAirDate
}

type tbcplSeasonResponse struct {
	Name         string         `json:"name"`
	SeasonNumber int            `json:"season_number"`
	Episodes     []tbcplEpisode `json:"episodes"`
}

type tbcplEpisode struct {
	EpisodeNumber int    `json:"episode_number"`
	SeasonNumber  int    `json:"season_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	Runtime       int    `json:"runtime"`
}

type tbcplDirectServer struct {
	Name string
	SR   string
}

var tbcplDirectServers = []tbcplDirectServer{
	{Name: "Togi", SR: "0"},
	{Name: "Achilles", SR: "3"},
	{Name: "Nflix", SR: "4"},
	{Name: "Drag", SR: "5"},
}

// Search uses TMDB's no-key web search endpoint for reliable title lookup.
// The returned TMDB IDs are then used by 1Shows/Vidzee.
func (t *TBCPL) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search/trending?query=%s",
		strings.TrimRight(t.tmdbBaseURL, "/"), url.QueryEscape(query))

	body, err := t.fetch(searchURL, strings.TrimRight(t.tmdbBaseURL, "/")+"/", "application/json, text/html, */*")
	if err != nil {
		return nil, fmt.Errorf("tbcpl search: %w", err)
	}

	results, err := t.parseCatalogResponse(body, "")
	if err != nil {
		return nil, fmt.Errorf("tbcpl search: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}
	return results, nil
}

// GetDetails returns cached metadata from search/trending results. 1Shows detail
// endpoints currently reject direct server-side requests, so this is best-effort.
func (t *TBCPL) GetDetails(id string) (*media.ContentDetail, error) {
	t.mu.RLock()
	item, ok := t.searchCache[id]
	t.mu.RUnlock()
	if !ok {
		return &media.ContentDetail{}, nil
	}

	return &media.ContentDetail{
		Description: item.Overview,
		Rating:      formatTBCPLRating(item.VoteAverage),
		Released:    item.released(),
	}, nil
}

// GetSeasons returns available TV seasons by probing 1Shows' season API.
func (t *TBCPL) GetSeasons(id string) ([]media.Season, error) {
	tmdbID := extractTMDBID(id)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, err
	}

	var seasons []media.Season
	for season := 1; season <= tbcplMaxSeasons; season++ {
		resp, err := t.fetchSeason(tmdbID, season)
		if err != nil {
			if season == 1 {
				return nil, fmt.Errorf("getting seasons: %w", err)
			}
			break
		}
		if len(resp.Episodes) == 0 {
			break
		}

		number := resp.SeasonNumber
		if number == 0 {
			number = season
		}
		seasons = append(seasons, media.Season{
			Number: number,
			ID:     fmt.Sprintf("%s:%d", tmdbID, number),
		})
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found")
	}
	return seasons, nil
}

// GetEpisodes returns episodes for a TV season from 1Shows' season API.
func (t *TBCPL) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	tmdbID, season, err := parseTBCPLSeasonID(id, seasonID)
	if err != nil {
		return nil, err
	}

	resp, err := t.fetchSeason(tmdbID, season)
	if err != nil {
		return nil, fmt.Errorf("getting episodes: %w", err)
	}

	episodes := make([]media.Episode, 0, len(resp.Episodes))
	for _, ep := range resp.Episodes {
		seasonNum := ep.SeasonNumber
		if seasonNum == 0 {
			seasonNum = season
		}
		if ep.EpisodeNumber <= 0 {
			continue
		}
		episodes = append(episodes, media.Episode{
			Number: ep.EpisodeNumber,
			Title:  ep.Name,
			ID:     fmt.Sprintf("%s:%d:%d", tmdbID, seasonNum, ep.EpisodeNumber),
		})
	}
	if len(episodes) == 0 {
		return nil, fmt.Errorf("no episodes found")
	}
	return episodes, nil
}

// GetServers returns direct Vidzee server choices. Watch performs the actual
// stream resolution and decryption.
func (t *TBCPL) GetServers(id string, episodeID string) ([]media.Server, error) {
	servers := make([]media.Server, 0, len(tbcplDirectServers))
	for _, srv := range tbcplDirectServers {
		servers = append(servers, media.Server{
			Name: srv.Name,
			ID:   "vidzee:" + srv.SR,
		})
	}
	return servers, nil
}

// GetEmbedURL supports callers that treat server IDs as public embed URLs.
// TBCPL's direct Vidzee servers must use Watch instead.
func (t *TBCPL) GetEmbedURL(serverID string) (string, error) {
	if strings.HasPrefix(serverID, "https://") {
		return serverID, nil
	}
	return "", fmt.Errorf("use Watch for TBCPL server %q", serverID)
}

// Trending returns 1Shows trending titles for the requested media type.
func (t *TBCPL) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	apiURL := fmt.Sprintf("%s/api/trending/%s/day", t.baseURL(), mt)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return nil, fmt.Errorf("tbcpl trending: %w", err)
	}
	return t.parseCatalogResponse(body, mt)
}

// Recent returns recent-ish content using the 1Shows weekly trending endpoint.
func (t *TBCPL) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	apiURL := fmt.Sprintf("%s/api/trending/%s/week", t.baseURL(), mt)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return t.Trending(mediaType)
	}
	return t.parseCatalogResponse(body, mt)
}

// Watch resolves a direct HLS stream through Vidzee's server API.
func (t *TBCPL) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	candidates := tbcplDirectServers
	if srv, ok := t.matchDirectServer(server); ok {
		candidates = []tbcplDirectServer{srv}
	}

	var lastErr error
	for _, srv := range candidates {
		stream, err := t.watchWithServer(mediaID, episodeID, srv, quality)
		if err == nil {
			return stream, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("tbcpl watch failed: %w", lastErr)
}

func (t *TBCPL) parseCatalogResponse(body []byte, fallbackMediaType string) ([]media.SearchResult, error) {
	var resp tbcplSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	results := make([]media.SearchResult, 0, len(resp.Results))
	for _, raw := range resp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}

		var item tbcplCatalogItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.ID == 0 {
			continue
		}
		if item.MediaType == "" {
			item.MediaType = fallbackMediaType
		}
		if item.MediaType != "movie" && item.MediaType != "tv" {
			continue
		}
		title := item.displayTitle()
		if title == "" {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}
		id := fmt.Sprintf("%s/%d", item.MediaType, item.ID)
		t.cacheCatalogItem(id, item)

		results = append(results, media.SearchResult{
			ID:    id,
			Title: title,
			Type:  mt,
			Year:  item.year(),
			URL:   fmt.Sprintf("%s/%s/%d", t.baseURL(), item.MediaType, item.ID),
		})
	}

	return results, nil
}

func (t *TBCPL) cacheCatalogItem(id string, item tbcplCatalogItem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.searchCache[id] = item
}

func (t *TBCPL) fetchSeason(tmdbID string, season int) (*tbcplSeasonResponse, error) {
	key := fmt.Sprintf("%s:%d", tmdbID, season)

	t.mu.RLock()
	cached := t.seasonCache[key]
	t.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	apiURL := fmt.Sprintf("%s/api/tv/%s/season/%d", t.baseURL(), tmdbID, season)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return nil, err
	}

	var resp tbcplSeasonResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing season response: %w", err)
	}

	t.mu.Lock()
	t.seasonCache[key] = &resp
	t.mu.Unlock()

	return &resp, nil
}

func (t *TBCPL) watchWithServer(mediaID, episodeID string, server tbcplDirectServer, quality string) (*media.Stream, error) {
	tmdbID := extractTMDBID(mediaID)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, err
	}

	values := url.Values{}
	values.Set("id", tmdbID)
	values.Set("sr", server.SR)

	if episodeID != "" {
		epID, season, episode, err := parseTBCPLEpisodeID(mediaID, episodeID)
		if err != nil {
			return nil, err
		}
		values.Set("id", epID)
		values.Set("ss", strconv.Itoa(season))
		values.Set("ep", strconv.Itoa(episode))
	}

	apiURL := fmt.Sprintf("%s/api/server?%s", strings.TrimRight(t.vidzeeBaseURL, "/"), values.Encode())
	body, err := t.fetch(apiURL, strings.TrimRight(t.vidzeeBaseURL, "/")+"/", "application/json, */*")
	if err != nil {
		return nil, err
	}

	var resp tbcplVidzeeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing Vidzee response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("Vidzee %s: %s", server.Name, resp.Error)
	}
	if len(resp.URL) == 0 {
		return nil, fmt.Errorf("Vidzee %s returned no URLs", server.Name)
	}

	key, err := t.fetchVidzeeKey()
	if err != nil {
		return nil, err
	}

	for _, source := range resp.URL {
		streamURL, err := decryptTBCPLVidzeeLink(source.Link, key)
		if err != nil || streamURL == "" {
			continue
		}

		return &media.Stream{
			URL:       streamURL,
			Quality:   quality,
			Subtitles: mapTBCPLVidzeeTracks(resp.Tracks),
			Referer:   strings.TrimRight(t.vidzeeBaseURL, "/") + "/",
		}, nil
	}

	return nil, fmt.Errorf("Vidzee %s returned no decryptable stream URLs", server.Name)
}

type tbcplVidzeeResponse struct {
	Error  string              `json:"error"`
	URL    []tbcplVidzeeSource `json:"url"`
	Tracks []tbcplVidzeeTrack  `json:"tracks"`
}

type tbcplVidzeeSource struct {
	Lang string `json:"lang"`
	Link string `json:"link"`
	Type string `json:"type"`
	Name string `json:"name"`
}

type tbcplVidzeeTrack struct {
	Lang string `json:"lang"`
	URL  string `json:"url"`
}

func mapTBCPLVidzeeTracks(tracks []tbcplVidzeeTrack) []media.Subtitle {
	subs := make([]media.Subtitle, 0, len(tracks))
	for _, tr := range tracks {
		if tr.URL == "" {
			continue
		}
		label := tr.Lang
		if label == "" {
			label = "Subtitle"
		}
		subs = append(subs, media.Subtitle{
			Language: tr.Lang,
			Label:    label,
			URL:      tr.URL,
		})
	}
	return subs
}

func (t *TBCPL) fetchVidzeeKey() (string, error) {
	t.mu.RLock()
	cached := t.vidzeeKey
	t.mu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	body, err := t.fetch(t.vidzeeKeyURL, strings.TrimRight(t.vidzeeBaseURL, "/")+"/", "text/plain, */*")
	if err != nil {
		return "", fmt.Errorf("fetching Vidzee key: %w", err)
	}

	key, err := decryptTBCPLVidzeeKey(strings.TrimSpace(string(body)))
	if err != nil {
		return "", fmt.Errorf("decrypting Vidzee key: %w", err)
	}

	t.mu.Lock()
	t.vidzeeKey = key
	t.mu.Unlock()

	return key, nil
}

func (t *TBCPL) matchDirectServer(server string) (tbcplDirectServer, bool) {
	server = strings.TrimSpace(server)
	if server == "" || strings.EqualFold(server, "default") {
		return tbcplDirectServer{}, false
	}
	if strings.HasPrefix(strings.ToLower(server), "vidzee:") {
		server = strings.TrimPrefix(strings.ToLower(server), "vidzee:")
	}

	for _, srv := range tbcplDirectServers {
		if strings.EqualFold(server, srv.Name) || server == srv.SR {
			return srv, true
		}
	}
	return tbcplDirectServer{}, false
}

func (t *TBCPL) fetch(rawURL, referer, accept string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

func parseTBCPLSeasonID(mediaID, seasonID string) (string, int, error) {
	tmdbID := extractTMDBID(mediaID)
	rawSeason := strings.TrimSpace(seasonID)
	if rawSeason == "" {
		rawSeason = "1"
	}

	if strings.Contains(rawSeason, ":") {
		parts := strings.Split(rawSeason, ":")
		if len(parts) >= 2 {
			if parts[0] != "" {
				tmdbID = parts[0]
			}
			rawSeason = parts[len(parts)-1]
		}
	}

	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", 0, err
	}
	season, err := strconv.Atoi(rawSeason)
	if err != nil || season <= 0 {
		return "", 0, fmt.Errorf("invalid season ID: %s", seasonID)
	}
	return tmdbID, season, nil
}

func parseTBCPLEpisodeID(mediaID, episodeID string) (string, int, int, error) {
	tmdbID := extractTMDBID(mediaID)
	raw := strings.TrimSpace(episodeID)
	if raw == "" {
		return "", 0, 0, fmt.Errorf("episode ID cannot be empty")
	}

	var seasonPart, episodePart string
	switch {
	case strings.Contains(raw, ":"):
		parts := strings.Split(raw, ":")
		if len(parts) < 3 {
			return "", 0, 0, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		tmdbID = parts[0]
		seasonPart = parts[len(parts)-2]
		episodePart = parts[len(parts)-1]
	case strings.Contains(raw, "."):
		parts := strings.SplitN(raw, ".", 2)
		seasonPart = parts[0]
		episodePart = parts[1]
	default:
		seasonPart = "1"
		episodePart = raw
	}

	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", 0, 0, err
	}
	season, err := strconv.Atoi(seasonPart)
	if err != nil || season <= 0 {
		return "", 0, 0, fmt.Errorf("invalid season in episode ID: %s", episodeID)
	}
	episode, err := strconv.Atoi(episodePart)
	if err != nil || episode <= 0 {
		return "", 0, 0, fmt.Errorf("invalid episode in episode ID: %s", episodeID)
	}
	return tmdbID, season, episode, nil
}

func formatTBCPLRating(rating float64) string {
	if rating <= 0 {
		return ""
	}
	out := strconv.FormatFloat(rating, 'f', 1, 64)
	return strings.TrimSuffix(out, ".0")
}

func decryptTBCPLVidzeeKey(encrypted string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	if len(raw) <= 28 {
		return "", fmt.Errorf("encrypted key too short")
	}

	iv := raw[:12]
	tag := raw[12:28]
	ciphertext := raw[28:]
	payload := append(append([]byte{}, ciphertext...), tag...)

	key := sha256.Sum256([]byte(tbcplVidzeeKeySecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plain, err := gcm.Open(nil, iv, payload, nil)
	if err != nil {
		return "", err
	}
	if len(plain) == 0 {
		return "", fmt.Errorf("empty key")
	}
	return string(plain), nil
}

func decryptTBCPLVidzeeLink(encrypted, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid encrypted link")
	}

	iv, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}

	aesKey := []byte(key)
	if len(aesKey) < 32 {
		padded := make([]byte, 32)
		copy(padded, aesKey)
		aesKey = padded
	}
	if len(aesKey) != 16 && len(aesKey) != 24 && len(aesKey) != 32 {
		return "", fmt.Errorf("invalid Vidzee link key length %d", len(aesKey))
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("invalid ciphertext length")
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded data length")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padLen], nil
}
