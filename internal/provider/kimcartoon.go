package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// KimCartoon implements the Provider interface for kimcartoon.com.co.
// This provider serves cartoons and anime content.
type KimCartoon struct {
	base   string // e.g., "kimcartoon.com.co"
	client *http.Client
}

// NewKimCartoon creates a new KimCartoon provider.
func NewKimCartoon(base string) *KimCartoon {
	return &KimCartoon{
		base:   base,
		client: httputil.NewClient(),
	}
}

func (k *KimCartoon) baseURL() string {
	return "https://" + k.base
}

// Search returns matching results for a query.
func (k *KimCartoon) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/?s=%s", k.baseURL(), url.QueryEscape(query))

	doc, err := k.fetchDocument(searchURL)
	if err != nil {
		return nil, fmt.Errorf("searching for %q: %w", query, err)
	}

	results := parseKCSearchResults(doc)

	// Fetch page 2 if available
	if doc.Find("a.next.page-numbers").Length() > 0 {
		page2URL := fmt.Sprintf("%s/page/2/?s=%s", k.baseURL(), url.QueryEscape(query))
		if doc2, err := k.fetchDocument(page2URL); err == nil {
			results = append(results, parseKCSearchResults(doc2)...)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}

	sortByRelevance(results, query)

	return results, nil
}

// parseKCSearchResults extracts search results from a kimcartoon search page.
func parseKCSearchResults(doc *goquery.Document) []media.SearchResult {
	var results []media.SearchResult

	doc.Find("article.bs div.bsx a[itemprop='url']").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		title := strings.TrimSpace(s.AttrOr("title", ""))
		if title == "" {
			title = strings.TrimSpace(s.Find(".ttzz").Text())
		}

		id := extractKCID(href)
		if id == "" {
			return
		}

		year := ""
		if m := kcYearRe.FindStringSubmatch(title); len(m) > 1 {
			year = m[1]
		}

		results = append(results, media.SearchResult{
			ID:    id,
			Title: title,
			Type:  media.TV, // Cartoons are episodic
			Year:  year,
			URL:   href,
		})
	})

	return results
}

var kcYearRe = regexp.MustCompile(`\((\d{4})\)`)

// extractKCID extracts the path-based ID from a kimcartoon URL.
// e.g., "https://kimcartoon.com.co/cartoon/south-park-season-28-2025/" -> "cartoon/south-park-season-28-2025"
func extractKCID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return ""
	}
	return path
}

// GetDetails returns detailed metadata for a content item.
func (k *KimCartoon) GetDetails(id string) (*media.ContentDetail, error) {
	pageURL := fmt.Sprintf("%s/%s/", k.baseURL(), id)
	doc, err := k.fetchDocument(pageURL)
	if err != nil {
		return nil, fmt.Errorf("getting details: %w", err)
	}

	return parseKCDetail(doc), nil
}

// parseKCDetail extracts content details from a kimcartoon show page.
func parseKCDetail(doc *goquery.Document) *media.ContentDetail {
	detail := &media.ContentDetail{}

	detail.Description = strings.TrimSpace(doc.Find("div.mindesc").Text())

	doc.Find("div.genxed a").Each(func(_ int, s *goquery.Selection) {
		detail.Genre = append(detail.Genre, strings.TrimSpace(s.Text()))
	})

	doc.Find("div.spe span").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if strings.Contains(text, "Status:") {
			detail.Released = strings.TrimSpace(strings.TrimPrefix(text, "Status:"))
		}
	})

	return detail
}

// GetSeasons returns a single synthetic season since kimcartoon treats
// each season as a separate show entry.
func (k *KimCartoon) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}

// GetEpisodes returns episodes for the given show/season page.
func (k *KimCartoon) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	pageURL := fmt.Sprintf("%s/%s/", k.baseURL(), seasonID)
	doc, err := k.fetchDocument(pageURL)
	if err != nil {
		return nil, fmt.Errorf("getting episodes: %w", err)
	}

	episodes := parseKCEpisodes(doc)
	if len(episodes) == 0 {
		return nil, fmt.Errorf("no episodes found")
	}

	return episodes, nil
}

var kcEpNumRe = regexp.MustCompile(`Episode\s+(\d+)`)

// parseKCEpisodes extracts episodes from a kimcartoon show page.
// The site lists episodes newest-first, so we reverse to chronological order.
func parseKCEpisodes(doc *goquery.Document) []media.Episode {
	var episodes []media.Episode

	doc.Find("div.eplister ul#myList li a").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		titleText := strings.TrimSpace(s.Find("div.epl-title").Text())

		num := 0
		if m := kcEpNumRe.FindStringSubmatch(titleText); len(m) > 1 {
			num, _ = strconv.Atoi(m[1])
		}

		epID := extractKCID(href)
		if epID == "" {
			return
		}

		episodes = append(episodes, media.Episode{
			Number: num,
			Title:  titleText,
			ID:     epID,
		})
	})

	// Reverse to chronological order (site lists newest first)
	for i, j := 0, len(episodes)-1; i < j; i, j = i+1, j-1 {
		episodes[i], episodes[j] = episodes[j], episodes[i]
	}

	// If episode numbers weren't parsed, assign sequential numbers
	for i := range episodes {
		if episodes[i].Number == 0 {
			episodes[i].Number = i + 1
		}
	}

	return episodes
}

// GetServers returns available streaming servers for an episode.
// episodeID is the path to the episode page.
func (k *KimCartoon) GetServers(id string, episodeID string) ([]media.Server, error) {
	if episodeID == "" {
		episodeID = id
	}

	pageURL := fmt.Sprintf("%s/%s/", k.baseURL(), episodeID)
	doc, err := k.fetchDocument(pageURL)
	if err != nil {
		return nil, fmt.Errorf("fetching episode page: %w", err)
	}

	// Extract data-embed from the player div
	embedURL, exists := doc.Find("div.pembed").Attr("data-embed")
	if !exists || embedURL == "" {
		return nil, fmt.Errorf("no embed URL found on episode page")
	}

	// Build servers for each known backend
	servers := []media.Server{
		{Name: "TServer", ID: embedURL},
	}

	// Add alternate servers by appending server param
	base := embedURL
	if strings.Contains(base, "?") {
		base += "&"
	} else {
		base += "?"
	}
	servers = append(servers,
		media.Server{Name: "VHServer", ID: base + "s=vhserver"},
		media.Server{Name: "HServer", ID: base + "s=hserver"},
	)

	return servers, nil
}

var vidwishIframeRe = regexp.MustCompile(`<iframe[^>]*src="(https://player\.vidwish\.live[^"]+)"`)

// GetEmbedURL resolves the stream.php page to get the vidwish.live player URL.
func (k *KimCartoon) GetEmbedURL(serverID string) (string, error) {
	if serverID == "" {
		return "", fmt.Errorf("server ID cannot be empty")
	}

	// serverID is the full stream.php URL — fetch it to find the vidwish iframe
	req, err := http.NewRequest(http.MethodGet, serverID, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Referer", k.baseURL()+"/")

	resp, err := k.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching stream page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("stream page returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading stream page: %w", err)
	}

	match := vidwishIframeRe.FindStringSubmatch(string(body))
	if len(match) < 2 {
		return "", fmt.Errorf("no vidwish player URL found")
	}

	return match[1], nil
}

// Trending returns trending content from the homepage.
func (k *KimCartoon) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	doc, err := k.fetchDocument(k.baseURL() + "/")
	if err != nil {
		return nil, fmt.Errorf("getting trending: %w", err)
	}

	return parseKCSearchResults(doc), nil
}

// Recent returns recently added content.
func (k *KimCartoon) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return k.Trending(mediaType)
}

// fetchDocument fetches a URL and parses it into a goquery Document.
func (k *KimCartoon) fetchDocument(rawURL string) (*goquery.Document, error) {
	resp, err := httputil.Get(k.client, rawURL)
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
