// Package provider defines the interface for media content providers
// and their implementations.
package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// Consumet implements the Provider interface using the Consumet REST API.
type Consumet struct {
	baseURL string
	client  *http.Client
}

// NewConsumet creates a new Consumet provider targeting the given base URL.
func NewConsumet(baseURL string) *Consumet {
	return &Consumet{
		baseURL: baseURL,
		client:  httputil.NewClient(),
	}
}

// fetchJSON fetches a URL and returns the raw JSON bytes.
func (c *Consumet) fetchJSON(rawURL string) ([]byte, error) {
	return httputil.GetJSON(c.client, rawURL)
}

// consumetSearchResponse is the JSON structure returned by the search endpoint.
type consumetSearchResponse struct {
	CurrentPage int                    `json:"currentPage"`
	HasNextPage bool                   `json:"hasNextPage"`
	Results     []consumetSearchResult `json:"results"`
}

type consumetSearchResult struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	ReleaseDate string `json:"releaseDate"`
	Seasons     int    `json:"seasons"`
}

// Search returns matching results for the given query.
func (c *Consumet) Search(query string) ([]media.SearchResult, error) {
	encoded := httputil.EncodeQuery(query)
	endpoint := fmt.Sprintf("%s/movies/flixhq/%s", c.baseURL, encoded)

	body, err := c.fetchJSON(endpoint)
	if err != nil {
		return nil, fmt.Errorf("consumet search: %w", err)
	}

	var resp consumetSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("consumet search: parsing response: %w", err)
	}

	results := make([]media.SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		mt := media.Movie
		if r.Type == "TV Series" {
			mt = media.TV
		}
		results = append(results, media.SearchResult{
			ID:      r.ID,
			Title:   r.Title,
			Type:    mt,
			Year:    r.ReleaseDate,
			Seasons: r.Seasons,
		})
	}

	return results, nil
}

// consumetInfoResponse is the JSON structure returned by the info endpoint.
type consumetInfoResponse struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	ReleaseDate string            `json:"releaseDate"`
	Genres      []string          `json:"genres"`
	Casts       []string          `json:"casts"`
	Country     string            `json:"country"`
	Duration    string            `json:"duration"`
	Rating      json.Number       `json:"rating"`
	Episodes    []consumetEpisode `json:"episodes"`
}

type consumetEpisode struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Number int    `json:"number"`
	Season int    `json:"season"`
}

// fetchInfo fetches content info from the Consumet info endpoint.
func (c *Consumet) fetchInfo(id string) (*consumetInfoResponse, error) {
	endpoint := fmt.Sprintf("%s/movies/flixhq/info?id=%s", c.baseURL, url.QueryEscape(id))

	body, err := c.fetchJSON(endpoint)
	if err != nil {
		return nil, fmt.Errorf("consumet info: %w", err)
	}

	var info consumetInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("consumet info: parsing response: %w", err)
	}

	return &info, nil
}

// GetDetails returns detailed metadata for a content item.
func (c *Consumet) GetDetails(id string) (*media.ContentDetail, error) {
	info, err := c.fetchInfo(id)
	if err != nil {
		return nil, err
	}

	return &media.ContentDetail{
		Description: info.Description,
		Rating:      info.Rating.String(),
		Duration:    info.Duration,
		Genre:       info.Genres,
		Released:    info.ReleaseDate,
		Country:     info.Country,
		Casts:       info.Casts,
	}, nil
}

// GetSeasons returns available seasons derived from the episodes list.
func (c *Consumet) GetSeasons(id string) ([]media.Season, error) {
	info, err := c.fetchInfo(id)
	if err != nil {
		return nil, err
	}

	seen := make(map[int]bool)
	var seasons []media.Season
	for _, ep := range info.Episodes {
		if !seen[ep.Season] {
			seen[ep.Season] = true
			seasons = append(seasons, media.Season{
				Number: ep.Season,
				ID:     fmt.Sprintf("%d", ep.Season),
			})
		}
	}

	return seasons, nil
}

// GetEpisodes returns episodes for a given season (seasonID is a string number like "1").
func (c *Consumet) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	info, err := c.fetchInfo(id)
	if err != nil {
		return nil, err
	}

	var seasonNum int
	if _, err := fmt.Sscanf(seasonID, "%d", &seasonNum); err != nil {
		return nil, fmt.Errorf("invalid season ID %q: %w", seasonID, err)
	}

	var episodes []media.Episode
	for _, ep := range info.Episodes {
		if ep.Season == seasonNum {
			episodes = append(episodes, media.Episode{
				Number: ep.Number,
				Title:  ep.Title,
				ID:     ep.ID,
			})
		}
	}

	return episodes, nil
}

// consumetServerResponse is an item in the servers list.
type consumetServerResponse struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	ID   string `json:"id"`
}

// GetServers returns available streaming servers for content.
func (c *Consumet) GetServers(id string, episodeID string) ([]media.Server, error) {
	endpoint := fmt.Sprintf("%s/movies/flixhq/servers?episodeId=%s&mediaId=%s",
		c.baseURL, url.QueryEscape(episodeID), url.QueryEscape(id))

	body, err := c.fetchJSON(endpoint)
	if err != nil {
		return nil, fmt.Errorf("consumet servers: %w", err)
	}

	var raw []consumetServerResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("consumet servers: parsing response: %w", err)
	}

	servers := make([]media.Server, 0, len(raw))
	for _, s := range raw {
		servers = append(servers, media.Server{
			Name: s.Name,
			ID:   s.ID,
		})
	}

	return servers, nil
}

// GetEmbedURL is not used with the Consumet provider — streaming goes through Watch.
func (c *Consumet) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("GetEmbedURL not supported by consumet provider; use Watch instead")
}

// consumetWatchResponse is the JSON structure returned by the watch endpoint.
type consumetWatchResponse struct {
	Sources   []consumetSource   `json:"sources"`
	Subtitles []consumetSubtitle `json:"subtitles"`
}

type consumetSource struct {
	URL     string `json:"url"`
	Quality string `json:"quality"`
	IsM3U8  bool   `json:"isM3U8"`
}

type consumetSubtitle struct {
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

// Watch resolves a stream for the given media/episode/server/quality combination.
func (c *Consumet) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	endpoint := fmt.Sprintf("%s/movies/flixhq/watch?episodeId=%s&mediaId=%s&server=%s",
		c.baseURL, url.QueryEscape(episodeID), url.QueryEscape(mediaID), url.QueryEscape(server))

	body, err := c.fetchJSON(endpoint)
	if err != nil {
		return nil, fmt.Errorf("consumet watch: %w", err)
	}

	var resp consumetWatchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("consumet watch: parsing response: %w", err)
	}

	if len(resp.Sources) == 0 {
		return nil, fmt.Errorf("consumet watch: no sources returned")
	}

	// Pick source matching the requested quality; fall back to first.
	chosen := resp.Sources[0]
	for _, s := range resp.Sources {
		if containsStr(s.Quality, quality) {
			chosen = s
			break
		}
	}

	subtitles := make([]media.Subtitle, 0, len(resp.Subtitles))
	for _, sub := range resp.Subtitles {
		subtitles = append(subtitles, media.Subtitle{
			Language: sub.Lang,
			Label:    sub.Lang,
			URL:      sub.URL,
		})
	}

	return &media.Stream{
		URL:       chosen.URL,
		Quality:   chosen.Quality,
		Subtitles: subtitles,
	}, nil
}

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	if substr == "" {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Trending is not supported by the Consumet API provider.
func (c *Consumet) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("trending not supported by consumet API provider")
}

// Recent is not supported by the Consumet API provider.
func (c *Consumet) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("recent not supported by consumet API provider")
}
