package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const vaplayerReferer = "https://nextgencloudfabric.com/"

// vaplayerBase is a var so tests can point it at a stub server.
var vaplayerBase = "https://streamdata.vaplayer.ru/api.php"

// VaPlayer implements the StreamProvider interface using the vaplayer
// streaming API. It accepts TMDB IDs and returns direct HLS m3u8 URLs.
type VaPlayer struct {
	client *http.Client
}

// NewVaPlayer creates a new VaPlayer provider.
func NewVaPlayer() *VaPlayer {
	return &VaPlayer{
		client: httputil.NewClient(),
	}
}

// vaplayerResponse is the JSON response from the vaplayer API.
// status_code is a string ("200") on success but a bare number (404) on
// failure, so it must be decoded as json.Number to handle both.
type vaplayerResponse struct {
	StatusCode json.Number `json:"status_code"`
	Data       struct {
		Title      string   `json:"title"`
		IMDBID     string   `json:"imdb_id"`
		FileName   string   `json:"file_name"`
		Backdrop   string   `json:"backdrop"`
		StreamURLs []string `json:"stream_urls"`
		Season     string   `json:"season"`
		Episode    string   `json:"episode"`
		Subtitles  []struct {
			URL      string `json:"url"`
			Language string `json:"language"`
			Label    string `json:"label"`
		} `json:"subtitles"`
	} `json:"data"`
}

// Search uses TMDB's keyless multi-search endpoint (same as Soap2Day/VidNest).
func (vp *VaPlayer) Search(query string) ([]media.SearchResult, error) {
	req, err := http.NewRequest(http.MethodGet, tmdbMultiSearchURL(tmdbBaseURL, query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Referer", tmdbBaseURL+"/")

	resp, err := vp.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vaplayer search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vaplayer search: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("vaplayer search: %w", err)
	}

	results, err := parseTMDBSearchResults(body, tmdbBaseURL)
	if err != nil {
		return nil, fmt.Errorf("vaplayer search: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%w for %q", ErrNoResults, query)
	}
	return results, nil
}

// GetDetails returns minimal metadata.
func (vp *VaPlayer) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

// GetSeasons returns seasons by probing the vaplayer API for each season.
func (vp *VaPlayer) GetSeasons(id string) ([]media.Season, error) {
	tmdbID := extractTMDBID(id)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, fmt.Errorf("vaplayer: invalid TMDB ID: %w", err)
	}

	var seasons []media.Season
	for n := 1; n <= 25; n++ {
		apiURL := fmt.Sprintf("%s?tmdb=%s&type=tv&season=%d&episode=1", vaplayerBase, tmdbID, n)
		resp, err := vp.fetchAPI(apiURL)
		if err != nil || len(resp.Data.StreamURLs) == 0 {
			if n == 1 {
				// No season 1 = not a TV show
				return nil, fmt.Errorf("no seasons found")
			}
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

// GetEpisodes returns episodes for a season by probing the API.
func (vp *VaPlayer) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	parts := strings.SplitN(seasonID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid season ID: %s", seasonID)
	}
	tmdbID := parts[0]
	seasonNum, _ := strconv.Atoi(parts[1])

	var episodes []media.Episode
	for ep := 1; ep <= 50; ep++ {
		apiURL := fmt.Sprintf("%s?tmdb=%s&type=tv&season=%d&episode=%d", vaplayerBase, tmdbID, seasonNum, ep)
		resp, err := vp.fetchAPI(apiURL)
		if err != nil || len(resp.Data.StreamURLs) == 0 {
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

// GetServers returns a single server.
// GetServers lists one entry per stream URL the API offers. They are separate
// mirrors and, in practice, sometimes separate dubs — collapsing them to a
// single "VaPlayer" server meant only the first was ever reachable, so an
// English film served Hindi-first had no alternative to fall back to.
func (vp *VaPlayer) GetServers(id string, episodeID string) ([]media.Server, error) {
	apiURL, err := vp.apiURLFor(id, episodeID)
	if err != nil {
		return nil, err
	}
	resp, err := vp.fetchAPI(apiURL)
	if err != nil {
		return nil, fmt.Errorf("vaplayer: %w", err)
	}
	if len(resp.Data.StreamURLs) == 0 {
		return nil, fmt.Errorf("vaplayer: no stream URLs")
	}
	servers := make([]media.Server, 0, len(resp.Data.StreamURLs))
	for i := range resp.Data.StreamURLs {
		servers = append(servers, media.Server{
			Name: fmt.Sprintf("Source %d", i+1),
			ID:   strconv.Itoa(i),
		})
	}
	return servers, nil
}

// vaplayerSourceIndex maps a server name back to its stream_urls index. An
// unknown name (the resolver and watch history both pass names this provider
// never issued) keeps the first source rather than failing.
func vaplayerSourceIndex(server string, n int) int {
	var i int
	name := strings.TrimSpace(server)
	if _, err := fmt.Sscanf(name, "Source %d", &i); err != nil {
		return 0
	}
	if i < 1 || i > n {
		return 0
	}
	// Sscanf stops after %d and ignores anything that follows, so names like
	// "Source 2 legacy" would otherwise select source 2. Only a name this
	// provider actually issued may pick a source; everything else falls back.
	if name != fmt.Sprintf("Source %d", i) {
		return 0
	}
	return i - 1
}

// GetEmbedURL is not used for this provider.
func (vp *VaPlayer) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}

// Watch resolves a stream URL through the vaplayer API.
// apiURLFor builds the vaplayer API URL for a movie or a specific episode.
func (vp *VaPlayer) apiURLFor(mediaID, episodeID string) (string, error) {
	tmdbID := extractTMDBID(mediaID)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", fmt.Errorf("vaplayer: invalid TMDB ID: %w", err)
	}

	var apiURL string
	if episodeID != "" {
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return "", fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		if err := httputil.ValidateNumericID(parts[0]); err != nil {
			return "", fmt.Errorf("vaplayer: invalid TMDB ID in episode: %w", err)
		}
		seasonNum, err := strconv.Atoi(parts[1])
		if err != nil || seasonNum <= 0 {
			return "", fmt.Errorf("vaplayer: invalid season in episode ID: %s", episodeID)
		}
		episodeNum, err := strconv.Atoi(parts[2])
		if err != nil || episodeNum <= 0 {
			return "", fmt.Errorf("vaplayer: invalid episode in episode ID: %s", episodeID)
		}
		apiURL = fmt.Sprintf("%s?tmdb=%s&type=tv&season=%s&episode=%s",
			vaplayerBase, parts[0], parts[1], parts[2])
	} else {
		apiURL = fmt.Sprintf("%s?tmdb=%s&type=movie", vaplayerBase, tmdbID)
	}
	return apiURL, nil
}

// Watch resolves a stream URL through the vaplayer API. server selects which of
// the API's stream_urls to use ("Source N", as listed by GetServers); anything
// unrecognised keeps the first, which is the historical behaviour.
func (vp *VaPlayer) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	apiURL, err := vp.apiURLFor(mediaID, episodeID)
	if err != nil {
		return nil, err
	}

	resp, err := vp.fetchAPI(apiURL)
	if err != nil {
		return nil, fmt.Errorf("vaplayer: %w", err)
	}

	if len(resp.Data.StreamURLs) == 0 {
		return nil, fmt.Errorf("vaplayer: no stream URLs")
	}

	// Map subtitles
	var subs []media.Subtitle
	for _, s := range resp.Data.Subtitles {
		if s.URL != "" {
			label := s.Label
			if label == "" {
				label = s.Language
			}
			subs = append(subs, media.Subtitle{
				Language: s.Language,
				Label:    label,
				URL:      s.URL,
			})
		}
	}

	return &media.Stream{
		URL:       resp.Data.StreamURLs[vaplayerSourceIndex(server, len(resp.Data.StreamURLs))],
		Quality:   quality,
		Subtitles: subs,
		Referer:   vaplayerReferer,
	}, nil
}

// Trending returns trending content from TMDB.
func (vp *VaPlayer) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}

	trendingURL := fmt.Sprintf("%s/search/trending?query=a", tmdbSearchBase)
	req, err := http.NewRequest(http.MethodGet, trendingURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Referer", tmdbSearchBase+"/")

	resp, err := vp.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, err
	}

	var tmdbResp tmdbSearchResponse
	if err := json.Unmarshal(body, &tmdbResp); err != nil {
		return nil, err
	}

	var results []media.SearchResult
	for _, raw := range tmdbResp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var item tmdbSearchResult
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.MediaType == "" {
			item.MediaType = mt
		}
		if item.MediaType != mt {
			continue
		}
		resultType := media.Movie
		if item.MediaType == "tv" {
			resultType = media.TV
		}
		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:  item.displayTitle(),
			Type:   resultType,
			Year:   item.year(),
			URL:    fmt.Sprintf("%s/%s/%d", tmdbSearchBase, item.MediaType, item.ID),
			Poster: tmdbPosterURL(item.PosterPath),
		})
	}
	return results, nil
}

// Recent returns recently added content (uses trending).
func (vp *VaPlayer) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return vp.Trending(mediaType)
}

// fetchAPI makes a request to the vaplayer API and returns the parsed response.
func (vp *VaPlayer) fetchAPI(apiURL string) (*vaplayerResponse, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("Referer", vaplayerReferer)

	resp, err := vp.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result vaplayerResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.StatusCode.String() != "200" {
		return nil, fmt.Errorf("API error: status %s", result.StatusCode)
	}

	return &result, nil
}
