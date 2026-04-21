// Package provider defines the interface for media content providers
// and their implementations.
package provider

import (
	"encoding/json"
	"fmt"
	"net/http"

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

// GetDetails is not yet implemented.
func (c *Consumet) GetDetails(id string) (*media.ContentDetail, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetSeasons is not yet implemented.
func (c *Consumet) GetSeasons(id string) ([]media.Season, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetEpisodes is not yet implemented.
func (c *Consumet) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	return nil, fmt.Errorf("not implemented")
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
