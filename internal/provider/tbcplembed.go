package provider

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"lobster/internal/extract"
	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/tbcpl"
)

// TBCPLEmbed plays trusted, otherwise-unsupported TBCPL movie/anime sites that
// follow the common TMDB-id embed convention. Discovery is TMDB-driven (like
// VidNest/VaPlayer); stream resolution (Task 8's Watch) sniffs an <iframe> from
// a templated embed URL and hands it to the extract package.
//
// It is used only as a resolver fallback (a StreamProvider): the resolver calls
// Search then Watch. GetSeasons/GetEpisodes/GetServers are not on that path and
// are minimal stubs.
type TBCPLEmbed struct {
	client  *http.Client
	sites   []tbcpl.Site
	log     func(string, ...any)
	quality string
	resolve func(embed, referer string) (*media.Stream, error)
}

// NewTBCPLEmbed keeps only movie/anime sites from the supplied catalog slice.
func NewTBCPLEmbed(sites []tbcpl.Site) *TBCPLEmbed {
	var kept []tbcpl.Site
	for _, s := range sites {
		if s.Category == "movies" || s.Category == "anime" {
			kept = append(kept, s)
		}
	}
	return &TBCPLEmbed{
		client: httputil.NewClient(),
		sites:  kept,
		log:    func(string, ...any) {},
	}
}

// Search uses TMDB's keyless multi-search endpoint (same as VidNest/Soap2Day).
func (p *TBCPLEmbed) Search(query string) ([]media.SearchResult, error) {
	body, err := p.fetchTMDB(tmdbMultiSearchURL(tmdbBaseURL, query))
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	results, err := parseTMDBSearchResults(body, tmdbBaseURL)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}
	return results, nil
}

func (p *TBCPLEmbed) fetchTMDB(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", tmdbSearchBase+"/")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

func (p *TBCPLEmbed) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

// GetSeasons is a stub: TBCPLEmbed resolves via Watch on the resolver fallback
// path, which supplies season/episode directly. It is not used as a primary
// browse provider.
func (p *TBCPLEmbed) GetSeasons(id string) ([]media.Season, error) {
	return nil, fmt.Errorf("tbcplembed: browse unsupported; resolved via Watch fallback")
}

func (p *TBCPLEmbed) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return nil, fmt.Errorf("tbcplembed: browse unsupported; resolved via Watch fallback")
}

func (p *TBCPLEmbed) GetServers(id, episodeID string) ([]media.Server, error) {
	return nil, fmt.Errorf("tbcplembed: use Watch instead")
}

func (p *TBCPLEmbed) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("tbcplembed: use Watch instead")
}

func (p *TBCPLEmbed) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func (p *TBCPLEmbed) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

var iframeSrcRe = regexp.MustCompile(`(?i)<iframe[^>]+src=["']([^"']+)["']`)

// embedCandidates returns templated embed URLs for a site origin + TMDB id.
func embedCandidates(origin, tmdbID string, season, episode int) []string {
	origin = strings.TrimRight(origin, "/")
	if season > 0 {
		s, e := strconv.Itoa(season), strconv.Itoa(episode)
		return []string{
			origin + "/embed/tv/" + tmdbID + "/" + s + "/" + e,
			origin + "/tv/" + tmdbID + "/" + s + "/" + e,
			origin + "/e/" + tmdbID + "/" + s + "/" + e,
		}
	}
	return []string{
		origin + "/embed/movie/" + tmdbID,
		origin + "/movie/" + tmdbID,
		origin + "/e/" + tmdbID,
	}
}

// parseSeasonEpisode parses an episodeID of the form "<tmdb>:<season>:<episode>".
// Returns (0, 0) when the id has fewer than 3 colon-separated parts (movie).
func parseSeasonEpisode(episodeID string) (int, int) {
	parts := strings.Split(episodeID, ":")
	if len(parts) < 3 {
		return 0, 0
	}
	season, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0
	}
	episode, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0
	}
	return season, episode
}

// siteOrigin returns the scheme+host portion of a raw URL, with no path.
func siteOrigin(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.TrimRight(rawURL, "/")
	}
	return u.Scheme + "://" + u.Host
}

// absoluteURL resolves a possibly-relative iframe src against a site origin.
func absoluteURL(origin, src string) string {
	switch {
	case strings.HasPrefix(src, "//"):
		return "https:" + src
	case strings.HasPrefix(src, "/"):
		return origin + src
	default:
		return src
	}
}

// fetchBytes performs a simple GET with a browser User-Agent.
func fetchBytes(client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

func (p *TBCPLEmbed) resolveDefault(embed, referer string) (*media.Stream, error) {
	ex, target := extract.ResolveForURL(embed, referer)
	return ex.Extract(target, p.quality)
}

// Watch tries each candidate site's embed templates until one yields a stream.
func (p *TBCPLEmbed) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.quality = quality
	if p.resolve == nil {
		p.resolve = p.resolveDefault
	}
	tmdbID := extractTMDBID(mediaID)
	season, episode := parseSeasonEpisode(episodeID)

	for _, s := range p.sites {
		origin := siteOrigin(s.URL)
		found := false
		for _, cand := range embedCandidates(origin, tmdbID, season, episode) {
			page, err := fetchBytes(p.client, cand)
			if err != nil {
				continue
			}
			m := iframeSrcRe.FindSubmatch(page)
			if m == nil {
				continue
			}
			embed := absoluteURL(origin, string(m[1]))
			stream, err := p.resolve(embed, origin+"/")
			if err != nil || stream == nil || stream.URL == "" {
				continue
			}
			found = true
			return stream, nil
		}
		if !found {
			p.log("tbcplembed: no playable embed from %s", s.URL)
		}
	}
	return nil, fmt.Errorf("tbcplembed: no site produced a stream")
}
