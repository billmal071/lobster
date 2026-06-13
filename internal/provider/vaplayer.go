package provider

import (
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
	vaplayerBase    = "https://streamdata.vaplayer.ru/api.php"
	vaplayerReferer = "https://nextgencloudfabric.com/"
)

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
type vaplayerResponse struct {
	StatusCode string `json:"status_code"`
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

// Search uses TMDB's no-key web search endpoint (same as Soap2Day/VidNest).
func (vp *VaPlayer) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search/trending?query=%s",
		tmdbSearchBase, url.QueryEscape(query))

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Referer", tmdbSearchBase+"/")

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

	var tmdbResp tmdbSearchResponse
	if err := json.Unmarshal(body, &tmdbResp); err != nil {
		return nil, fmt.Errorf("vaplayer search: %w", err)
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
		if item.MediaType != "movie" && item.MediaType != "tv" {
			continue
		}
		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}
		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:  item.displayTitle(),
			Type:   mt,
			Year:   item.year(),
			URL:    fmt.Sprintf("%s/%s/%d", tmdbSearchBase, item.MediaType, item.ID),
			Poster: tmdbPosterURL(item.PosterPath),
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
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
func (vp *VaPlayer) GetServers(id string, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "VaPlayer", ID: "default"}}, nil
}

// GetEmbedURL is not used for this provider.
func (vp *VaPlayer) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}

// Watch resolves a stream URL through the vaplayer API.
func (vp *VaPlayer) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	tmdbID := extractTMDBID(mediaID)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, fmt.Errorf("vaplayer: invalid TMDB ID: %w", err)
	}

	var apiURL string
	if episodeID != "" {
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		if err := httputil.ValidateNumericID(parts[0]); err != nil {
			return nil, fmt.Errorf("vaplayer: invalid TMDB ID in episode: %w", err)
		}
		seasonNum, err := strconv.Atoi(parts[1])
		if err != nil || seasonNum <= 0 {
			return nil, fmt.Errorf("vaplayer: invalid season in episode ID: %s", episodeID)
		}
		episodeNum, err := strconv.Atoi(parts[2])
		if err != nil || episodeNum <= 0 {
			return nil, fmt.Errorf("vaplayer: invalid episode in episode ID: %s", episodeID)
		}
		apiURL = fmt.Sprintf("%s?tmdb=%s&type=tv&season=%s&episode=%s",
			vaplayerBase, parts[0], parts[1], parts[2])
	} else {
		apiURL = fmt.Sprintf("%s?tmdb=%s&type=movie", vaplayerBase, tmdbID)
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
		URL:       resp.Data.StreamURLs[0],
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

	if result.StatusCode != "200" {
		return nil, fmt.Errorf("API error: status %s", result.StatusCode)
	}

	return &result, nil
}
