package provider

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// plURLPattern matches the inline script variable that holds the AJAX URL for server listings.
// Movies use "vdkz" and TV episodes use "vds" as the query parameter.
var plURLPattern = regexp.MustCompile(`const\s+pl_url\s*=\s*'([^']+)'`)

// FlixHQWS implements the Provider interface for the flixhq.ws content source.
type FlixHQWS struct {
	base   string // e.g., "flixhq.ws"
	client *http.Client
}

// NewFlixHQWS creates a new FlixHQWS provider.
func NewFlixHQWS(base string) *FlixHQWS {
	return &FlixHQWS{
		base:   base,
		client: httputil.NewClient(),
	}
}

func (f *FlixHQWS) baseURL() string {
	return "https://" + f.base
}

// Search returns matching results for a query, fetching multiple pages.
func (f *FlixHQWS) Search(query string) ([]media.SearchResult, error) {
	// flixhq.ws uses space-encoded queries (%20), not dash-separated like flixhq.to
	encoded := url.PathEscape(query)
	baseSearchURL := fmt.Sprintf("%s/search/%s/", f.baseURL(), encoded)

	doc, err := f.fetchDocument(baseSearchURL)
	if err != nil {
		return nil, fmt.Errorf("searching for %q: %w", query, err)
	}

	results := parseSearchResults(doc)
	lastPage := parseLastPage(doc)

	pages := lastPage
	if pages > maxSearchPages {
		pages = maxSearchPages
	}
	for page := 2; page <= pages; page++ {
		pageURL := fmt.Sprintf("%s?page=%d", baseSearchURL, page)
		pageDoc, err := f.fetchDocument(pageURL)
		if err != nil {
			break
		}
		results = append(results, parseSearchResults(pageDoc)...)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}

	sortByRelevance(results, query)

	for i := range results {
		if !strings.HasPrefix(results[i].URL, "http") {
			results[i].URL = f.baseURL() + results[i].URL
		}
	}

	return results, nil
}

// GetDetails returns detailed metadata for a content item.
func (f *FlixHQWS) GetDetails(id string) (*media.ContentDetail, error) {
	if err := httputil.ValidateID(id); err != nil {
		return nil, fmt.Errorf("invalid content ID: %w", err)
	}

	url := fmt.Sprintf("%s/%s/", f.baseURL(), id)
	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting details: %w", err)
	}

	return parseDetailPage(doc), nil
}

// GetSeasons returns available seasons for a TV show.
// On flixhq.ws, seasons are embedded in the detail page as .ss-item elements
// inside a .dropdown-menu, each with data-ss (season number) and data-id (hash).
func (f *FlixHQWS) GetSeasons(id string) ([]media.Season, error) {
	if err := httputil.ValidateID(id); err != nil {
		return nil, fmt.Errorf("invalid content ID: %w", err)
	}

	url := fmt.Sprintf("%s/%s/", f.baseURL(), id)
	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting seasons: %w", err)
	}

	return parseWSSeason(doc), nil
}

// parseWSSeason extracts seasons from flixhq.ws detail page HTML.
func parseWSSeason(doc *goquery.Document) []media.Season {
	var seasons []media.Season

	doc.Find(".dropdown-menu .ss-item").Each(func(_ int, s *goquery.Selection) {
		dataSS, _ := s.Attr("data-ss")
		dataID, _ := s.Attr("data-id")

		num, _ := strconv.Atoi(dataSS)

		if dataID != "" {
			seasons = append(seasons, media.Season{
				Number: num,
				ID:     dataID,
			})
		}
	})

	return seasons
}

// GetEpisodes returns episodes for a given season.
// seasonID is the data-id hash from GetSeasons. We fetch the AJAX endpoint
// to get episode HTML fragments.
func (f *FlixHQWS) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	if seasonID == "" {
		return nil, fmt.Errorf("season ID cannot be empty")
	}

	url := fmt.Sprintf("%s/ajax/ajax.php?episode=%s", f.baseURL(), seasonID)
	doc, err := f.fetchDocument(url)
	if err != nil {
		return nil, fmt.Errorf("getting episodes: %w", err)
	}

	return parseWSEpisodes(doc), nil
}

// parseWSEpisodes extracts episodes from flixhq.ws AJAX episode response.
func parseWSEpisodes(doc *goquery.Document) []media.Episode {
	var episodes []media.Episode

	doc.Find("li.nav-item a.eps-item").Each(func(_ int, s *goquery.Selection) {
		dataID, exists := s.Attr("data-id")
		if !exists {
			return
		}

		title := strings.TrimSpace(s.AttrOr("title", ""))
		if title == "" {
			title = strings.TrimSpace(s.Text())
		}

		// Extract the href for use as the episode identifier in GetServers.
		// The href has the form /series/{slug}-{id}/{season}-{episode}/
		href, _ := s.Attr("href")

		num := 0
		text := strings.TrimSpace(s.Text())
		if parts := strings.Fields(text); len(parts) >= 2 {
			candidate := strings.TrimRight(parts[1], ":")
			if n, err := strconv.Atoi(candidate); err == nil {
				num = n
			} else if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				num = n
			}
		}

		// Use href as the episode ID if available so GetServers can fetch
		// the episode page to extract the pl_url. Fall back to data-id.
		epID := href
		if epID == "" {
			epID = dataID
		}

		episodes = append(episodes, media.Episode{
			Number: num,
			Title:  title,
			ID:     epID,
		})
	})

	return episodes
}

// GetServers returns available streaming servers for content.
// For movies (episodeID is empty), fetches the movie page and extracts pl_url.
// For TV (episodeID is the episode page path), fetches the episode page.
func (f *FlixHQWS) GetServers(id string, episodeID string) ([]media.Server, error) {
	var pageURL string

	if episodeID != "" {
		// TV episode: episodeID is the episode page path (e.g., /series/batman-19660/1-3/)
		if strings.HasPrefix(episodeID, "/") {
			pageURL = f.baseURL() + episodeID
		} else {
			pageURL = fmt.Sprintf("%s/%s", f.baseURL(), episodeID)
		}
	} else {
		// Movie
		if err := httputil.ValidateID(id); err != nil {
			return nil, fmt.Errorf("invalid content ID: %w", err)
		}
		pageURL = fmt.Sprintf("%s/%s/", f.baseURL(), id)
	}

	// Ensure trailing slash
	if !strings.HasSuffix(pageURL, "/") {
		pageURL += "/"
	}

	doc, err := f.fetchDocument(pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetching content page: %w", err)
	}

	// Extract pl_url from inline <script> tags.
	plURL := ""
	doc.Find("script").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		if matches := plURLPattern.FindStringSubmatch(text); len(matches) > 1 {
			plURL = matches[1]
		}
	})

	if plURL == "" {
		return nil, fmt.Errorf("no server listing URL found on page")
	}

	serverDoc, err := f.fetchDocument(plURL)
	if err != nil {
		return nil, fmt.Errorf("fetching server list: %w", err)
	}

	return parseWSServers(serverDoc), nil
}

// parseWSServers extracts servers from flixhq.ws AJAX server response.
func parseWSServers(doc *goquery.Document) []media.Server {
	var servers []media.Server

	doc.Find(".sv-item").Each(func(_ int, s *goquery.Selection) {
		dataID, exists := s.Attr("data-id")
		if !exists {
			return
		}

		name := strings.TrimSpace(s.AttrOr("data-srv", ""))
		if name == "" {
			name = strings.TrimSpace(s.Text())
		}
		if name == "" {
			name = "Unknown"
		}

		servers = append(servers, media.Server{
			Name: name,
			ID:   dataID,
		})
	})

	return servers
}

// GetEmbedURL returns the embed URL for a given server.
// On flixhq.ws, the server ID from GetServers is already the embed URL.
func (f *FlixHQWS) GetEmbedURL(serverID string) (string, error) {
	if serverID == "" {
		return "", fmt.Errorf("server ID cannot be empty")
	}
	return serverID, nil
}

// Trending returns trending content from the /home page.
func (f *FlixHQWS) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	url := fmt.Sprintf("%s/home/", f.baseURL())

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

// Recent returns recently added content from /movie or /series pages.
func (f *FlixHQWS) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	var url string
	switch mediaType {
	case media.Movie:
		url = fmt.Sprintf("%s/movie/", f.baseURL())
	case media.TV:
		url = fmt.Sprintf("%s/series/", f.baseURL())
	default:
		url = fmt.Sprintf("%s/movie/", f.baseURL())
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
func (f *FlixHQWS) fetchDocument(url string) (*goquery.Document, error) {
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
