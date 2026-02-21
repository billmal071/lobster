package provider

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// FlixHQ implements the Provider interface for the FlixHQ content source.
type FlixHQ struct {
	base   string // e.g., "flixhq.to"
	client *http.Client
}

// NewFlixHQ creates a new FlixHQ provider.
func NewFlixHQ(base string) *FlixHQ {
	return &FlixHQ{
		base:   base,
		client: httputil.NewClient(),
	}
}

func (f *FlixHQ) baseURL() string {
	return "https://" + f.base
}

// Search returns matching results for a query.
func (f *FlixHQ) Search(query string) ([]media.SearchResult, error) {
	encoded := httputil.EncodeQuery(query)
	url := fmt.Sprintf("%s/search/%s", f.baseURL(), encoded)

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("searching for %q: %w", query, err)
	}

	results := parseSearchResults(doc)
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}

	// Set full URLs
	for i := range results {
		if !strings.HasPrefix(results[i].URL, "http") {
			results[i].URL = f.baseURL() + results[i].URL
		}
	}

	return results, nil
}

// GetSeasons returns available seasons for a TV show.
func (f *FlixHQ) GetSeasons(id string) ([]media.Season, error) {
	if err := httputil.ValidateID(id); err != nil {
		return nil, fmt.Errorf("invalid content ID: %w", err)
	}

	numID := extractNumericID(id)
	if numID == "" {
		return nil, fmt.Errorf("cannot extract numeric ID from %q", id)
	}

	url := fmt.Sprintf("%s/ajax/v2/tv/seasons/%s", f.baseURL(), numID)
	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting seasons: %w", err)
	}

	return parseSeasons(doc), nil
}

// GetEpisodes returns episodes for a given season.
func (f *FlixHQ) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	if err := httputil.ValidateID(seasonID); err != nil {
		return nil, fmt.Errorf("invalid season ID: %w", err)
	}

	url := fmt.Sprintf("%s/ajax/v2/season/episodes/%s", f.baseURL(), seasonID)
	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting episodes: %w", err)
	}

	return parseEpisodes(doc), nil
}

// GetServers returns available streaming servers for content.
func (f *FlixHQ) GetServers(id string, episodeID string) ([]media.Server, error) {
	var url string

	if episodeID != "" {
		// TV episode
		if err := httputil.ValidateID(episodeID); err != nil {
			return nil, fmt.Errorf("invalid episode ID: %w", err)
		}
		url = fmt.Sprintf("%s/ajax/v2/episode/servers/%s", f.baseURL(), episodeID)
	} else {
		// Movie
		if err := httputil.ValidateID(id); err != nil {
			return nil, fmt.Errorf("invalid content ID: %w", err)
		}
		numID := extractNumericID(id)
		if numID == "" {
			return nil, fmt.Errorf("cannot extract numeric ID from %q", id)
		}
		url = fmt.Sprintf("%s/ajax/v2/movie/episodes/%s", f.baseURL(), numID)
	}

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting servers: %w", err)
	}

	return parseServers(doc), nil
}

// GetEmbedURL returns the embed URL for a given server.
func (f *FlixHQ) GetEmbedURL(serverID string) (string, error) {
	if err := httputil.ValidateID(serverID); err != nil {
		return "", fmt.Errorf("invalid server ID: %w", err)
	}

	url := fmt.Sprintf("%s/ajax/get_link/%s", f.baseURL(), serverID)
	resp, err := httputil.Get(f.client, url)
	if err != nil {
		return "", fmt.Errorf("getting embed URL: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("parsing embed response: %w", err)
	}

	// The response contains a redirect URL or iframe src
	embedURL := ""
	doc.Find("iframe").Each(func(_ int, s *goquery.Selection) {
		if src, exists := s.Attr("src"); exists {
			embedURL = src
		}
	})

	// Some endpoints return the URL directly in a link element
	if embedURL == "" {
		doc.Find("a").Each(func(_ int, s *goquery.Selection) {
			if href, exists := s.Attr("href"); exists && strings.HasPrefix(href, "http") {
				embedURL = href
			}
		})
	}

	// The AJAX endpoint may return the link as text content
	if embedURL == "" {
		text := strings.TrimSpace(doc.Text())
		if strings.HasPrefix(text, "http") {
			embedURL = strings.Fields(text)[0]
		}
	}

	if embedURL == "" {
		return "", fmt.Errorf("no embed URL found for server %s", serverID)
	}

	return embedURL, nil
}

// Trending returns trending content.
func (f *FlixHQ) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	var url string
	switch mediaType {
	case media.Movie:
		url = fmt.Sprintf("%s/trending-movies", f.baseURL())
	case media.TV:
		url = fmt.Sprintf("%s/trending-tv-shows", f.baseURL())
	default:
		url = fmt.Sprintf("%s/home", f.baseURL())
	}

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting trending: %w", err)
	}

	results := parseTrendingResults(doc)
	for i := range results {
		if !strings.HasPrefix(results[i].URL, "http") {
			results[i].URL = f.baseURL() + results[i].URL
		}
	}
	return results, nil
}

// Recent returns recently added content.
func (f *FlixHQ) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	var url string
	switch mediaType {
	case media.Movie:
		url = fmt.Sprintf("%s/recently-added-movies", f.baseURL())
	case media.TV:
		url = fmt.Sprintf("%s/recently-added-tv-shows", f.baseURL())
	default:
		url = fmt.Sprintf("%s/recently-added", f.baseURL())
	}

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting recent: %w", err)
	}

	results := parseTrendingResults(doc)
	for i := range results {
		if !strings.HasPrefix(results[i].URL, "http") {
			results[i].URL = f.baseURL() + results[i].URL
		}
	}
	return results, nil
}

// fetchDocument fetches a URL and parses it into a goquery Document.
func (f *FlixHQ) fetchDocument(url string) (*goquery.Document, error) {
	resp, err := httputil.Get(f.client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	return doc, nil
}
