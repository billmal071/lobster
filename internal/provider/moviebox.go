package provider

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/media"
)

// MovieBox implements the StreamProvider interface using the MovieBox V3 mobile API.
type MovieBox struct {
	baseURLs    []string
	client      *http.Client
	token       string // bearer token from x-user header
	pos         int    // current host index
	mu          sync.RWMutex
	searchCache map[string]mbSearchItem // subjectID -> item
}

// NewMovieBox creates a new MovieBox provider.
func NewMovieBox() *MovieBox {
	return &MovieBox{
		baseURLs: []string{
			"https://api3.aoneroom.com",
			"https://api4.aoneroom.com",
			"https://api5.aoneroom.com",
			"https://api6.aoneroom.com",
		},
		client:      &http.Client{Timeout: 30 * time.Second},
		searchCache: make(map[string]mbSearchItem),
	}
}

// baseURL returns the current API base URL.
func (m *MovieBox) baseURL() string {
	return m.baseURLs[m.pos%len(m.baseURLs)]
}

// rotateHost advances to the next host.
func (m *MovieBox) rotateHost() {
	m.pos++
}

const (
	hmacKey1  = "76iRl07s0xSN9jqmEWAt79EBJZulIQIsV64FZr2O"
	hmacKey2  = "Xqn2nnO41/L92o1iuXhSLHTbXvY4Z5ZZ62m8mSLA"
	mbAppID   = "302770f8bb6543ce8bdff585943a1eca"
	mbAppKey  = "a9d263ae575d4f5d94eab086a150c67e"
)

// sign signs a request with HMAC-MD5 and returns the required headers.
func (m *MovieBox) sign(method, path, body string) (clientToken, signature string) {
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)

	// X-Client-Token: {ts},{md5(reverse(ts))}
	rev := reverseString(ts)
	h := md5.Sum([]byte(rev))
	clientToken = ts + "," + hex.EncodeToString(h[:])

	// For GET requests (empty body), body hash and length are empty strings.
	// For POST requests, they are the MD5 hash and length of the body.
	var bodyLenStr, bodyHashStr string
	if body != "" {
		bodyMD5 := md5.Sum([]byte(body))
		bodyLenStr = strconv.Itoa(len(body))
		bodyHashStr = hex.EncodeToString(bodyMD5[:])
	}

	// Sort query parameters alphabetically in the canonical path.
	canonicalPath := sortQueryParams(path)

	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s\n%s",
		method,
		"application/json",
		"application/json",
		bodyLenStr,
		ts,
		bodyHashStr,
		canonicalPath,
	)

	key, _ := base64.StdEncoding.DecodeString(hmacKey1)
	mac := hmac.New(md5.New, key)
	mac.Write([]byte(canonical))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	signature = ts + "|2|" + sig

	return
}

// sortQueryParams sorts URL query parameters alphabetically for canonical signing.
func sortQueryParams(path string) string {
	idx := strings.IndexByte(path, '?')
	if idx == -1 {
		return path
	}
	basePath := path[:idx]
	query := path[idx+1:]
	params := strings.Split(query, "&")
	sort.Strings(params)
	return basePath + "?" + strings.Join(params, "&")
}

// request makes an HTTP request with MovieBox signing and returns the response body.
func (m *MovieBox) request(method, path string, body []byte) ([]byte, error) {
	var lastErr error

	bodyStr := ""
	if len(body) > 0 {
		bodyStr = string(body)
	}

	for i := 0; i < len(m.baseURLs); i++ {
		url := m.baseURL() + path

		var bodyReader io.Reader
		if bodyStr != "" {
			bodyReader = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		clientToken, signature := m.sign(method, path, bodyStr)
		req.Header.Set("X-Client-Token", clientToken)
		req.Header.Set("x-tr-signature", signature)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-client-status", "0")
		req.Header.Set("appid", mbAppID)
		req.Header.Set("appkey", mbAppKey)
		req.Header.Set("lang", "en")
		req.Header.Set("os", "android")

		m.mu.RLock()
		token := m.token
		m.mu.RUnlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := m.client.Do(req)
		if err != nil {
			lastErr = err
			m.rotateHost()
			continue
		}

		if resp.StatusCode >= 500 && i < len(m.baseURLs)-1 {
			resp.Body.Close()
			m.rotateHost()
			continue
		}

		// Capture bearer token from x-user header (JSON: {"token":"..."})
		if xUser := resp.Header.Get("x-user"); xUser != "" {
			m.extractToken(xUser)
		}

		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
		}

		body2, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}

		return body2, nil
	}

	return nil, fmt.Errorf("all hosts failed, last error: %w", lastErr)
}

// extractToken parses the x-user response header to capture the bearer token.
// The header may be a JSON object like {"token":"eyJ..."} or a plain token string.
func (m *MovieBox) extractToken(xUser string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" {
		return
	}

	xUser = strings.TrimSpace(xUser)
	if xUser == "" {
		return
	}

	// Try JSON format first
	if xUser[0] == '{' {
		var parsed struct {
			Token string `json:"token"`
		}
		if json.Unmarshal([]byte(xUser), &parsed) == nil && parsed.Token != "" {
			m.token = parsed.Token
			return
		}
	}

	// Fall back to using raw value as token
	m.token = xUser
}

// unwrapData extracts the "data" field from the API envelope.
// If the body is not wrapped in an envelope, it returns the body as-is.
func (m *MovieBox) unwrapData(body []byte) (json.RawMessage, error) {
	var envelope mbAPIResponse
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		// Not an envelope, return body as-is (legacy format).
		return body, nil
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("API error code %d: %s", envelope.Code, envelope.Message)
	}
	return envelope.Data, nil
}

// reverseString reverses a string.
func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// --- Response types ---

// mbAPIResponse wraps the top-level API envelope.
type mbAPIResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type mbSearchResponse struct {
	Results []mbSearchTopic `json:"results"`
}

type mbSearchTopic struct {
	TopicType string         `json:"topicType"`
	Subjects  []mbSearchItem `json:"subjects"`
}

type mbSearchItem struct {
	SubjectID   string `json:"subjectId"`
	Title       string `json:"title"`
	SubjectType int    `json:"subjectType"`
	ReleaseDate string `json:"releaseDate"`
	Duration    string `json:"duration"`
	Genre       string `json:"genre"`
	HasResource bool   `json:"hasResource"`
	SeNum       int    `json:"seNum"`
	IMDBRating  string `json:"imdbRatingValue"`
	CountryName string `json:"countryName"`
	Language    string `json:"language"`
	Description string `json:"description"`
	DetailURL   string `json:"detailUrl"`
	Cover       struct {
		URL string `json:"url"`
	} `json:"cover"`
}

// Note: Detail and season endpoints require authentication (407).
// Metadata is extracted from search results instead.

type mbPlayInfoResponse struct {
	HLS         []mbHLSEntry      `json:"hls"`
	Streams     []mbStreamEntry   `json:"streams"`
	Downloads   []mbDownloadEntry `json:"downloads"`
	HasResource bool              `json:"hasResource"`
}

type mbDownloadEntry struct {
	ID         string `json:"id"`
	URL        string `json:"url"`
	Resolution int    `json:"resolution"`
	Size       int64  `json:"size"`
}

type mbHLSEntry struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Quality string `json:"quality"`
}

type mbStreamEntry struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Quality string `json:"quality"`
}

type mbCaption struct {
	CaptionID   int64  `json:"captionId"`
	CaptionName string `json:"captionName"`
	Language    string `json:"language"`
	URL         string `json:"url"`
}

type mbCaptionsResponse struct {
	Captions []mbCaption `json:"captions"`
}

// --- Provider interface ---

// Search searches for content.
func (m *MovieBox) Search(query string) ([]media.SearchResult, error) {
	reqBody := fmt.Sprintf(`{"keyword":"%s","page":1,"perPage":20}`, query)
	body, err := m.request("POST", "/wefeed-mobile-bff/subject-api/search/v2", []byte(reqBody))
	if err != nil {
		return nil, fmt.Errorf("moviebox search: %w", err)
	}

	resp, err := m.unwrapData(body)
	if err != nil {
		return nil, fmt.Errorf("moviebox search: %w", err)
	}

	var searchResp mbSearchResponse
	if err := json.Unmarshal(resp, &searchResp); err != nil {
		return nil, fmt.Errorf("moviebox search: parsing data: %w", err)
	}

	var results []media.SearchResult
	for _, topic := range searchResp.Results {
		for _, item := range topic.Subjects {
			// Only include movies and TV shows (subjectType 1=movie, 2/3=TV)
			if item.SubjectType > 3 {
				continue
			}
			mt := mediaTypeFromSubjectType(item.SubjectType)
			year := ""
			if len(item.ReleaseDate) >= 4 {
				year = item.ReleaseDate[:4]
			}
			// Cache for GetDetails.
			m.mu.Lock()
			m.searchCache[item.SubjectID] = item
			m.mu.Unlock()

			results = append(results, media.SearchResult{
				ID:       item.SubjectID,
				Title:    item.Title,
				Type:     mt,
				Year:     year,
				Duration: item.Duration,
				Seasons:  item.SeNum,
				Poster:   item.Cover.URL,
			})
		}
	}

	return results, nil
}

// GetDetails returns detailed metadata for content.
// Uses cached search data since the detail endpoint requires authentication.
func (m *MovieBox) GetDetails(id string) (*media.ContentDetail, error) {
	m.mu.RLock()
	item, ok := m.searchCache[id]
	m.mu.RUnlock()

	if ok {
		var genres []string
		if item.Genre != "" {
			for _, g := range strings.Split(item.Genre, ", ") {
				genres = append(genres, strings.TrimSpace(g))
			}
		}
		return &media.ContentDetail{
			Description: item.Description,
			Rating:      item.IMDBRating,
			Duration:    item.Duration,
			Genre:       genres,
			Released:    item.ReleaseDate,
			Country:     item.CountryName,
		}, nil
	}

	// Fallback: search for the ID to populate cache.
	return &media.ContentDetail{}, nil
}

// GetSeasons returns available seasons for a TV show.
// Uses cached search data since the detail endpoint requires authentication.
func (m *MovieBox) GetSeasons(id string) ([]media.Season, error) {
	m.mu.RLock()
	item, ok := m.searchCache[id]
	m.mu.RUnlock()

	numSeasons := 1
	if ok && item.SeNum > 0 {
		numSeasons = item.SeNum
	}

	seasons := make([]media.Season, numSeasons)
	for i := 0; i < numSeasons; i++ {
		seasons[i] = media.Season{
			Number: i + 1,
			ID:     strconv.Itoa(i + 1),
		}
	}

	return seasons, nil
}

// GetEpisodes returns episodes for a given season.
// Generates a reasonable episode list since the detail endpoint requires authentication.
// The Watch method uses season.episode format, so exact episode IDs aren't critical.
func (m *MovieBox) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	seasonNum, _ := strconv.Atoi(seasonID)
	if seasonNum == 0 {
		seasonNum = 1
	}

	// Generate a reasonable number of episodes per season.
	numEpisodes := 10
	episodes := make([]media.Episode, numEpisodes)
	for i := 0; i < numEpisodes; i++ {
		epNum := i + 1
		episodes[i] = media.Episode{
			Number: epNum,
			Title:  fmt.Sprintf("Episode %d", epNum),
			ID:     fmt.Sprintf("%d.%d", seasonNum, epNum),
		}
	}

	return episodes, nil
}

// GetServers returns a synthetic server for direct streaming.
func (m *MovieBox) GetServers(id string, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "MovieBox", ID: "default"}}, nil
}

// GetEmbedURL is not used for MovieBox (uses Watch instead).
func (m *MovieBox) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("GetEmbedURL not supported by moviebox provider; use Watch instead")
}

// Trending returns trending content.
func (m *MovieBox) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	tabID := "movie"
	if mediaType == media.TV {
		tabID = "tv"
	}
	return m.fetchTabContent(tabID)
}

// Recent returns recently added content.
func (m *MovieBox) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	tabID := "movie"
	if mediaType == media.TV {
		tabID = "tv"
	}
	return m.fetchTabContent(tabID + "_new")
}

func (m *MovieBox) fetchTabContent(tabID string) ([]media.SearchResult, error) {
	body, err := m.request("GET", fmt.Sprintf("/wefeed-mobile-bff/tab-operating?page=1&tabId=%s&version=1", tabID), nil)
	if err != nil {
		return nil, fmt.Errorf("moviebox tab: %w", err)
	}

	data, err := m.unwrapData(body)
	if err != nil {
		return nil, fmt.Errorf("moviebox tab: %w", err)
	}

	// Tab content may come as a list of subjects or search-like results.
	var resp struct {
		Results []mbSearchTopic `json:"results"`
		List    []mbSearchItem  `json:"list"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("moviebox tab: parsing response: %w", err)
	}

	var items []mbSearchItem
	if len(resp.List) > 0 {
		items = resp.List
	} else {
		for _, topic := range resp.Results {
			items = append(items, topic.Subjects...)
		}
	}

	var results []media.SearchResult
	for _, item := range items {
		if item.SubjectType > 3 {
			continue
		}
		year := ""
		if len(item.ReleaseDate) >= 4 {
			year = item.ReleaseDate[:4]
		}
		results = append(results, media.SearchResult{
			ID:       item.SubjectID,
			Title:    item.Title,
			Type:     mediaTypeFromSubjectType(item.SubjectType),
			Year:     year,
			Duration: item.Duration,
			Seasons:  item.SeNum,
		})
	}

	return results, nil
}

// --- StreamProvider interface ---

// Watch resolves a direct stream URL.
func (m *MovieBox) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	se, ep := 0, 0

	if episodeID != "" {
		// Supports multiple formats:
		//   "season.episode" e.g. "1.5"
		//   "id:season:episode" e.g. "123:1:5" (fallback path)
		//   plain episode number e.g. "5"
		if strings.Contains(episodeID, ":") {
			parts := strings.Split(episodeID, ":")
			if len(parts) >= 3 {
				se, _ = strconv.Atoi(parts[len(parts)-2])
				ep, _ = strconv.Atoi(parts[len(parts)-1])
			}
		} else if strings.Contains(episodeID, ".") {
			parts := strings.Split(episodeID, ".")
			if len(parts) == 2 {
				se, _ = strconv.Atoi(parts[0])
				ep, _ = strconv.Atoi(parts[1])
			}
		} else {
			ep, _ = strconv.Atoi(episodeID)
		}
	}

	path := fmt.Sprintf("/wefeed-mobile-bff/subject-api/play-info?subjectId=%s&se=%d&ep=%d", mediaID, se, ep)
	body, err := m.request("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("moviebox watch: %w", err)
	}

	var resp mbPlayInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("moviebox watch: parsing response: %w", err)
	}

	if !resp.HasResource {
		return nil, fmt.Errorf("moviebox watch: no streams available (geo-restricted or unavailable)")
	}

	// Prefer HLS streams, fall back to MP4 streams, then downloads
	var streamURL, streamQuality string
	if len(resp.HLS) > 0 {
		sort.Slice(resp.HLS, func(i, j int) bool {
			return streamQualityRank(resp.HLS[i].Quality) > streamQualityRank(resp.HLS[j].Quality)
		})
		streamURL = resp.HLS[0].URL
		streamQuality = resp.HLS[0].Quality
	} else if len(resp.Streams) > 0 {
		sort.Slice(resp.Streams, func(i, j int) bool {
			return streamQualityRank(resp.Streams[i].Quality) > streamQualityRank(resp.Streams[j].Quality)
		})
		streamURL = resp.Streams[0].URL
		streamQuality = resp.Streams[0].Quality
	} else if len(resp.Downloads) > 0 {
		sort.Slice(resp.Downloads, func(i, j int) bool {
			return resp.Downloads[i].Resolution > resp.Downloads[j].Resolution
		})
		streamURL = resp.Downloads[0].URL
		streamQuality = fmt.Sprintf("%dp", resp.Downloads[0].Resolution)
	} else {
		return nil, fmt.Errorf("moviebox watch: no stream URLs found")
	}

	// Fetch subtitles if available
	subtitles := m.fetchSubtitles(mediaID)

	return &media.Stream{
		URL:       streamURL,
		Quality:   streamQuality,
		Subtitles: subtitles,
	}, nil
}

func (m *MovieBox) fetchSubtitles(mediaID string) []media.Subtitle {
	body, err := m.request("GET", "/wefeed-mobile-bff/subject-api/get-ext-captions?subjectId="+mediaID, nil)
	if err != nil {
		return nil
	}

	var resp mbCaptionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}

	subtitles := make([]media.Subtitle, 0, len(resp.Captions))
	for _, c := range resp.Captions {
		subtitles = append(subtitles, media.Subtitle{
			Language: c.Language,
			Label:    c.CaptionName,
			URL:      c.URL,
		})
	}

	return subtitles
}

// streamQualityRank returns a rank for quality sorting (higher = better).
func streamQualityRank(q string) int {
	switch {
	case strings.HasPrefix(q, "4k"):
		return 5
	case strings.HasPrefix(q, "1080"):
		return 4
	case strings.HasPrefix(q, "720"):
		return 3
	case strings.HasPrefix(q, "480"):
		return 2
	case strings.HasPrefix(q, "360"):
		return 1
	default:
		return 0
	}
}

// mediaTypeFromSubjectType converts MovieBox subjectType to media.MediaType.
func mediaTypeFromSubjectType(st int) media.MediaType {
	switch st {
	case 1:
		return media.Movie
	case 2, 3:
		return media.TV
	default:
		return media.Movie
	}
}