package provider

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/media"
)

// parseSearchResults extracts search results from a goquery document.
// Uses DOM parsing instead of sed/grep on raw HTML to prevent injection.
func parseSearchResults(doc *goquery.Document) []media.SearchResult {
	var results []media.SearchResult

	doc.Find(".film_list-wrap .flw-item").Each(func(_ int, s *goquery.Selection) {
		result := media.SearchResult{}

		// Extract title and URL from the link
		link := s.Find(".film-name a")
		result.Title = strings.TrimSpace(link.Text())
		href, exists := link.Attr("href")
		if exists {
			result.URL = href
			result.ID = extractID(href)
		}

		// Determine type from URL path or class
		if strings.Contains(href, "/tv/") {
			result.Type = media.TV
		} else {
			result.Type = media.Movie
		}

		// Extract metadata (year, seasons, episodes)
		s.Find(".fd-infor span").Each(func(_ int, span *goquery.Selection) {
			text := strings.TrimSpace(span.Text())
			if _, err := strconv.Atoi(text); err == nil && len(text) == 4 {
				result.Year = text
			}
		})

		if result.Title != "" {
			results = append(results, result)
		}
	})

	return results
}

// parseSeasons extracts season information from a show page.
func parseSeasons(doc *goquery.Document) []media.Season {
	var seasons []media.Season

	doc.Find(".dropdown-menu-model .dropdown-item a").Each(func(_ int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		dataID, _ := s.Attr("data-id")
		title := strings.TrimSpace(s.Text())

		num := 0
		if parts := strings.Fields(title); len(parts) >= 2 {
			num, _ = strconv.Atoi(parts[len(parts)-1])
		}

		if dataID == "" {
			// Try extracting from href
			parts := strings.Split(href, "/")
			if len(parts) > 0 {
				dataID = parts[len(parts)-1]
			}
		}

		seasons = append(seasons, media.Season{
			Number: num,
			ID:     dataID,
		})
	})

	return seasons
}

// parseEpisodes extracts episode information from a season page.
func parseEpisodes(doc *goquery.Document) []media.Episode {
	var episodes []media.Episode

	doc.Find(".nav-item a").Each(func(_ int, s *goquery.Selection) {
		dataID, exists := s.Attr("data-id")
		if !exists {
			return
		}

		title := strings.TrimSpace(s.AttrOr("title", ""))
		if title == "" {
			title = strings.TrimSpace(s.Text())
		}

		num := 0
		// Try to extract episode number from the title or text
		text := strings.TrimSpace(s.Text())
		if parts := strings.Fields(text); len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				num = n
			}
		}

		episodes = append(episodes, media.Episode{
			Number: num,
			Title:  title,
			ID:     dataID,
		})
	})

	return episodes
}

// parseServers extracts server options from a content page.
func parseServers(doc *goquery.Document) []media.Server {
	var servers []media.Server

	doc.Find(".server-item a, [data-id]").Each(func(_ int, s *goquery.Selection) {
		dataID, exists := s.Attr("data-id")
		if !exists {
			return
		}

		name := strings.TrimSpace(s.Text())
		if name == "" {
			name = s.AttrOr("title", "Unknown")
		}

		servers = append(servers, media.Server{
			Name: name,
			ID:   dataID,
		})
	})

	return servers
}

// extractID extracts the content ID from a URL path.
// e.g., "/movie/free-the-exorcist-hd-75043" -> "movie/free-the-exorcist-hd-75043"
func extractID(urlPath string) string {
	// Remove leading slash
	id := strings.TrimPrefix(urlPath, "/")
	// Remove any query parameters
	if idx := strings.Index(id, "?"); idx != -1 {
		id = id[:idx]
	}
	return id
}

// extractNumericID extracts the trailing numeric ID from a path.
// e.g., "movie/free-the-exorcist-hd-75043" -> "75043"
func extractNumericID(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if _, err := strconv.Atoi(last); err == nil {
			return last
		}
	}
	return ""
}

// parseTrendingResults extracts results from trending/recent pages.
func parseTrendingResults(doc *goquery.Document) []media.SearchResult {
	// The trending/recent pages use the same layout as search
	return parseSearchResults(doc)
}

// formatDisplayTitle creates a display string for fzf selection.
// FormatDisplayTitle creates a display string for fzf selection.
func FormatDisplayTitle(r media.SearchResult) string {
	parts := []string{r.Title}
	if r.Year != "" {
		parts = append(parts, fmt.Sprintf("(%s)", r.Year))
	}
	if r.Type == media.TV {
		parts = append(parts, "[TV]")
	} else {
		parts = append(parts, "[Movie]")
	}
	return strings.Join(parts, " ")
}
