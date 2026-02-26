package provider

import (
	"encoding/json"
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

// maxSearchPages limits how many pages of search results to fetch.
const maxSearchPages = 3

// Search returns matching results for a query, fetching multiple pages.
func (f *FlixHQ) Search(query string) ([]media.SearchResult, error) {
	encoded := httputil.EncodeQuery(query)
	baseSearchURL := fmt.Sprintf("%s/search/%s", f.baseURL(), encoded)

	// Fetch first page
	doc, err := f.fetchDocument(baseSearchURL)
	if err != nil {
		return nil, fmt.Errorf("searching for %q: %w", query, err)
	}

	results := parseSearchResults(doc)
	lastPage := parseLastPage(doc)

	// Fetch additional pages (up to maxSearchPages)
	pages := lastPage
	if pages > maxSearchPages {
		pages = maxSearchPages
	}
	for page := 2; page <= pages; page++ {
		pageURL := fmt.Sprintf("%s?page=%d", baseSearchURL, page)
		pageDoc, err := f.fetchDocument(pageURL)
		if err != nil {
			break // Stop on error but return what we have
		}
		results = append(results, parseSearchResults(pageDoc)...)
	}

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
		url = fmt.Sprintf("%s/ajax/movie/episodes/%s", f.baseURL(), numID)
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

	url := fmt.Sprintf("%s/ajax/episode/sources/%s", f.baseURL(), serverID)
	resp, err := httputil.Get(f.client, url)
	if err != nil {
		return "", fmt.Errorf("getting embed URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d for server %s", resp.StatusCode, serverID)
	}

	// The endpoint returns JSON: {"type":"iframe","link":"https://...","sources":[],"tracks":[],"title":""}
	var result struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing embed response: %w", err)
	}

	if result.Link == "" {
		return "", fmt.Errorf("no embed URL found for server %s", serverID)
	}

	return result.Link, nil
}

// Trending returns trending content from the /home page.
func (f *FlixHQ) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	url := fmt.Sprintf("%s/home", f.baseURL())

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting trending: %w", err)
	}

	results := parseTrendingResults(doc, mediaType)
	for i := range results {
		if !strings.HasPrefix(results[i].URL, "http") {
			results[i].URL = f.baseURL() + results[i].URL
		}
	}
	return results, nil
}

// Recent returns recently added content from /movie or /tv-show pages.
func (f *FlixHQ) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	var url string
	switch mediaType {
	case media.Movie:
		url = fmt.Sprintf("%s/movie", f.baseURL())
	case media.TV:
		url = fmt.Sprintf("%s/tv-show", f.baseURL())
	default:
		url = fmt.Sprintf("%s/movie", f.baseURL())
	}

	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting recent: %w", err)
	}

	results := parseSearchResults(doc)
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
