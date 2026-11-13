package provider

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// animeHeavenBase is the AnimeHeaven site root. A var so tests can point it at
// a stub.
var animeHeavenBase = "https://animeheaven.me"

// animeHeavenMaxBody caps a page read; the largest seen (a 220-episode show)
// is about 50 KB.
const animeHeavenMaxBody = 4 << 20

var (
	// Search rows: <div class='similarname c'><a href='anime.php?KEY' class='c'>Title</a>.
	ahSearchRe = regexp.MustCompile(`class='similarname c'><a href='anime\.php\?([a-z0-9]+)'[^>]*>([^<]+)</a>`)
	// Episode rows: the anchor's id is the 32-hex gate key, and the episode
	// number sits in the first watch2 cell after it.
	ahEpisodeRe = regexp.MustCompile(`(?s)<a [^>]*?id\s*="([0-9a-f]{32})"[^>]*>.*?watch2[^>]*>\s*([0-9.]+)\s*<`)
	ahCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	ahSourceRe  = regexp.MustCompile(`<source\s+src=['"](https?://[^'"]+)['"]`)
	ahKeyRe     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	ahShowIDRe  = regexp.MustCompile(`^[a-z0-9]{1,32}$`)
)

// AnimeHeaven resolves anime from animeheaven.me, a server-rendered site.
//
// The chain is plain HTTP, with no token exchange:
//
//	search.php?s=<query>      -> anime.php?<id> links
//	anime.php?<id>            -> one 32-hex gate key per episode, newest first
//	gate.php + Cookie: key=<k> -> a page whose <source> tags hold direct mp4 URLs
//
// The mp4 hosts accepted a request with no Referer when probed, so Stream
// carries none. Every show is one season; the site has no season split.
type AnimeHeaven struct {
	client *http.Client
}

func NewAnimeHeaven() *AnimeHeaven { return &AnimeHeaven{client: httputil.NewClient()} }

// get fetches a page, optionally with the gate cookie, and returns its body.
func (a *AnimeHeaven) get(rawURL, gateKey string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	if gateKey != "" {
		req.AddCookie(&http.Cookie{Name: "key", Value: gateKey})
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("animeheaven request: %w", httputil.CauseWithoutURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("animeheaven: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, animeHeavenMaxBody))
	if err != nil {
		return "", fmt.Errorf("reading animeheaven response: %w", err)
	}
	return string(body), nil
}

func (a *AnimeHeaven) root() string { return strings.TrimRight(animeHeavenBase, "/") }

// Search lists shows whose title matches the query. A repeated show id keeps
// its first row.
func (a *AnimeHeaven) Search(query string) ([]media.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	page, err := a.get(a.root()+"/search.php?s="+url.QueryEscape(query), "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []media.SearchResult
	for _, m := range ahSearchRe.FindAllStringSubmatch(page, -1) {
		id, title := m[1], strings.TrimSpace(html.UnescapeString(m[2]))
		if seen[id] || title == "" {
			continue
		}
		seen[id] = true
		out = append(out, media.SearchResult{
			ID:    id,
			Title: title,
			Type:  media.TV,
			URL:   a.root() + "/anime.php?" + id,
		})
	}
	return out, nil
}

// GetSeasons returns the single season every show has.
func (a *AnimeHeaven) GetSeasons(id string) ([]media.Season, error) {
	if !ahShowIDRe.MatchString(id) {
		return nil, fmt.Errorf("animeheaven: invalid show id")
	}
	return []media.Season{{Number: 1, ID: "1"}}, nil
}

// GetEpisodes lists a show's episodes in ascending order. Each Episode.ID is
// the gate key Watch needs. A number listed twice keeps its first key, and a
// fractional number (a recap such as 2.5) is skipped because Episode.Number is
// an integer.
func (a *AnimeHeaven) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	if !ahShowIDRe.MatchString(id) {
		return nil, fmt.Errorf("animeheaven: invalid show id")
	}
	page, err := a.get(a.root()+"/anime.php?"+id, "")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	out := []media.Episode{}
	for _, m := range ahEpisodeRe.FindAllStringSubmatch(page, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, media.Episode{Number: n, ID: m[1], Title: fmt.Sprintf("Episode %d", n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// GetServers returns the one source the site offers.
func (a *AnimeHeaven) GetServers(id string, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AnimeHeaven", ID: episodeID}}, nil
}

// Watch resolves an episode's gate key to a direct mp4 URL. It returns the
// first <source> outside an HTML comment that is not the page's error
// fallback; the site lists mirrors in that order.
func (a *AnimeHeaven) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	if !ahKeyRe.MatchString(episodeID) {
		return nil, fmt.Errorf("animeheaven: invalid episode key")
	}
	page, err := a.get(a.root()+"/gate.php", episodeID)
	if err != nil {
		return nil, err
	}
	page = ahCommentRe.ReplaceAllString(page, "")
	for _, m := range ahSourceRe.FindAllStringSubmatch(page, -1) {
		if strings.HasSuffix(m[1], "&error") || strings.HasSuffix(m[1], "&error2") {
			continue
		}
		return &media.Stream{URL: html.UnescapeString(m[1])}, nil
	}
	return nil, fmt.Errorf("animeheaven: no stream for episode")
}

func (a *AnimeHeaven) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

func (a *AnimeHeaven) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("animeheaven: use Watch instead")
}

func (a *AnimeHeaven) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func (a *AnimeHeaven) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
