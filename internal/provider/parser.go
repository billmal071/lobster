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

		// Extract metadata (year, duration, seasons, episodes)
		s.Find(".fd-infor span.fdi-item").Each(func(_ int, span *goquery.Selection) {
			text := strings.TrimSpace(span.Text())
			if _, err := strconv.Atoi(text); err == nil && len(text) == 4 {
				result.Year = text
			} else if strings.HasSuffix(text, "m") {
				result.Duration = text
			} else if strings.HasPrefix(text, "SS ") {
				if n, err := strconv.Atoi(strings.TrimPrefix(text, "SS ")); err == nil {
					result.Seasons = n
				}
			} else if strings.HasPrefix(text, "EPS ") {
				if n, err := strconv.Atoi(strings.TrimPrefix(text, "EPS ")); err == nil {
					result.Episodes = n
				}
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

	doc.Find(".dropdown-menu a.dropdown-item").Each(func(_ int, s *goquery.Selection) {
		dataID, _ := s.Attr("data-id")
		title := strings.TrimSpace(s.Text())

		num := 0
		if parts := strings.Fields(title); len(parts) >= 2 {
			num, _ = strconv.Atoi(parts[len(parts)-1])
		}

		if dataID == "" {
			// Try extracting from href
			href, exists := s.Attr("href")
			if exists {
				parts := strings.Split(href, "/")
				if len(parts) > 0 {
					dataID = parts[len(parts)-1]
				}
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
		// Try to extract episode number from the title or text.
		// FlixHQ format: "Eps 1: Title" or "Eps 1"
		text := strings.TrimSpace(s.Text())
		if parts := strings.Fields(text); len(parts) >= 2 {
			// Try "Eps N:" format first (strip trailing colon)
			candidate := strings.TrimRight(parts[1], ":")
			if n, err := strconv.Atoi(candidate); err == nil {
				num = n
			} else if n, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				// Fallback: last token as number
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
// Movie endpoints use data-linkid, TV episode endpoints use data-id.
func parseServers(doc *goquery.Document) []media.Server {
	var servers []media.Server

	doc.Find(".link-item, .server-item a, [data-id]").Each(func(_ int, s *goquery.Selection) {
		// Try data-linkid first (movie servers), then data-id (TV episode servers)
		dataID, exists := s.Attr("data-linkid")
		if !exists {
			dataID, exists = s.Attr("data-id")
		}
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

// parseDetailPage extracts detailed metadata from a content's detail page.
func parseDetailPage(doc *goquery.Document) *media.ContentDetail {
	detail := &media.ContentDetail{}

	// Description
	detail.Description = strings.TrimSpace(doc.Find(".description").First().Text())

	// Rating and duration from .stats spans
	doc.Find(".stats .item").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		// Rating: contains star icon, text is just the number
		if s.Find("i.fa-star, i.fas.fa-star").Length() > 0 {
			detail.Rating = strings.TrimSpace(strings.TrimPrefix(text, ""))
			// The icon text gets included, strip any non-numeric prefix
			for i, c := range detail.Rating {
				if c >= '0' && c <= '9' {
					detail.Rating = detail.Rating[i:]
					break
				}
			}
		} else if strings.Contains(text, "min") {
			detail.Duration = text
		}
	})

	// Genre, Released, Country, Casts from .elements .row-line
	doc.Find(".elements .row-line").Each(func(_ int, s *goquery.Selection) {
		label := strings.TrimSpace(s.Find(".type").Text())
		switch {
		case strings.HasPrefix(label, "Genre"):
			s.Find("a").Each(func(_ int, a *goquery.Selection) {
				if g := strings.TrimSpace(a.Text()); g != "" {
					detail.Genre = append(detail.Genre, g)
				}
			})
		case strings.HasPrefix(label, "Released"):
			// Text after the span
			full := strings.TrimSpace(s.Text())
			detail.Released = strings.TrimSpace(strings.TrimPrefix(full, label))
		case strings.HasPrefix(label, "Country"):
			detail.Country = strings.TrimSpace(s.Find("a").First().Text())
		case strings.HasPrefix(label, "Casts"):
			s.Find("a").Each(func(_ int, a *goquery.Selection) {
				if c := strings.TrimSpace(a.Text()); c != "" {
					detail.Casts = append(detail.Casts, c)
				}
			})
		}
	})

	return detail
}

// parseLastPage extracts the last page number from pagination links.
// Returns 1 if no pagination is found.
func parseLastPage(doc *goquery.Document) int {
	lastPage := 1
	doc.Find(".pagination .page-item a.page-link").Each(func(_ int, s *goquery.Selection) {
		title, _ := s.Attr("title")
		if title == "Last" {
			href, exists := s.Attr("href")
			if exists {
				if idx := strings.Index(href, "page="); idx >= 0 {
					pageStr := href[idx+5:]
					if n, err := strconv.Atoi(pageStr); err == nil {
						lastPage = n
					}
				}
			}
		}
	})
	return lastPage
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

// parseTrendingResults extracts results from the /home page's trending tab panels.
// It scopes to #trending-movies or #trending-tv based on mediaType, then parses
// the standard .film_list-wrap .flw-item structure within that panel.
func parseTrendingResults(doc *goquery.Document, mediaType media.MediaType) []media.SearchResult {
	var selector string
	switch mediaType {
	case media.Movie:
		selector = "#trending-movies"
	case media.TV:
		selector = "#trending-tv"
	default:
		// Fallback: parse the whole document
		return parseSearchResults(doc)
	}

	var results []media.SearchResult
	doc.Find(selector).Find(".film_list-wrap .flw-item").Each(func(_ int, s *goquery.Selection) {
		result := media.SearchResult{}

		link := s.Find(".film-name a")
		result.Title = strings.TrimSpace(link.Text())
		href, exists := link.Attr("href")
		if exists {
			result.URL = href
			result.ID = extractID(href)
		}

		if strings.Contains(href, "/tv/") {
			result.Type = media.TV
		} else {
			result.Type = media.Movie
		}

		s.Find(".fd-infor span.fdi-item").Each(func(_ int, span *goquery.Selection) {
			text := strings.TrimSpace(span.Text())
			if _, err := strconv.Atoi(text); err == nil && len(text) == 4 {
				result.Year = text
			} else if strings.HasSuffix(text, "m") {
				result.Duration = text
			} else if strings.HasPrefix(text, "SS ") {
				if n, err := strconv.Atoi(strings.TrimPrefix(text, "SS ")); err == nil {
					result.Seasons = n
				}
			} else if strings.HasPrefix(text, "EPS ") {
				if n, err := strconv.Atoi(strings.TrimPrefix(text, "EPS ")); err == nil {
					result.Episodes = n
				}
			}
		})

		if result.Title != "" {
			results = append(results, result)
		}
	})

	return results
}

// FormatDisplayTitle creates a display string for fzf selection.
func FormatDisplayTitle(r media.SearchResult) string {
	parts := []string{r.Title}
	if r.Year != "" {
		parts = append(parts, fmt.Sprintf("(%s)", r.Year))
	}
	if r.Type == media.TV {
		meta := "[TV"
		if r.Seasons > 0 {
			meta += fmt.Sprintf(" S:%d", r.Seasons)
		}
		if r.Episodes > 0 {
			meta += fmt.Sprintf(" Ep:%d", r.Episodes)
		}
		meta += "]"
		parts = append(parts, meta)
	} else {
		meta := "[Movie"
		if r.Duration != "" {
			meta += " " + r.Duration
		}
		meta += "]"
		parts = append(parts, meta)
	}
	return strings.Join(parts, " ")
}
