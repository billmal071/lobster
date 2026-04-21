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

// GetServers is not yet implemented.
func (c *Consumet) GetServers(id string, episodeID string) ([]media.Server, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetEmbedURL is not yet implemented.
func (c *Consumet) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// Watch is not yet implemented.
func (c *Consumet) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// Trending is not yet implemented.
func (c *Consumet) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

// Recent is not yet implemented.
func (c *Consumet) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}
