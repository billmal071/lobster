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
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// ahFoldDiacritics drops accents and macrons ("Shippūden" -> "Shippuden"),
// which the site's search does not fold itself. Case and punctuation are kept.
func ahFoldDiacritics(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// animeHeavenBase is the AnimeHeaven site root. A var so tests can point it at
// a stub.
var animeHeavenBase = "https://animeheaven.me"

// animeHeavenMaxBody caps a page read; the largest seen (a 220-episode show)
// is about 50 KB.
const animeHeavenMaxBody = 4 << 20

// animeHeavenUserAgent is sent on every request; the site serves its pages to
// browsers.
const animeHeavenUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0"

var (
	// ahSiteRe matches the search form in every real page's header. A block
	// page or captive portal answers 200 without it.
	ahSiteRe = regexp.MustCompile(`action='search\.php'`)
	// Search rows: <div class='similarname c'><a href='anime.php?KEY' class='c'>Title</a>.
	ahSearchRe = regexp.MustCompile(`class='similarname c'><a href='anime\.php\?([a-z0-9]{1,32})'[^>]*>([^<]+)</a>`)
	// Episode rows: the opening tag of an anchor whose id is the 32-hex gate
	// key. The episode number sits in a watch2 cell before the anchor closes.
	ahEpisodeRe = regexp.MustCompile(`<a\s[^>]*?\bid\s*="([0-9a-f]{32})"[^>]*>`)
	ahNumberRe  = regexp.MustCompile(`watch2[^>]*>\s*([0-9]+(?:\.[0-9]+)?)\s*<`)
	// A comment runs to its terminator, or to the end of the page when it has
	// none, as a browser reads it.
	ahCommentRe = regexp.MustCompile(`(?s)<!--.*?(?:-->|$)`)
	ahSourceRe  = regexp.MustCompile(`<source\s+src=['"]([^'"]+)['"]`)
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
// The request is built here rather than through httputil.Get because the gate
// needs a cookie. A body past animeHeavenMaxBody is an error: a truncated page
// would parse as a shorter catalogue.
func (a *AnimeHeaven) get(rawURL, gateKey string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", animeHeavenUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, animeHeavenMaxBody+1))
	if err != nil {
		return "", fmt.Errorf("reading animeheaven response: %w", err)
	}
	if len(body) > animeHeavenMaxBody {
		return "", fmt.Errorf("animeheaven: response larger than %d bytes", animeHeavenMaxBody)
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
	page, err := a.get(a.root()+"/search.php?s="+url.QueryEscape(ahFoldDiacritics(query)), "")
	if err != nil {
		return nil, err
	}
	if !ahSiteRe.MatchString(page) {
		return nil, fmt.Errorf("animeheaven: %w", ErrUnrecognisedResponse)
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
	if !ahSiteRe.MatchString(page) {
		return nil, fmt.Errorf("animeheaven: %w", ErrUnrecognisedResponse)
	}
	seen := map[int]bool{}
	out := []media.Episode{}
	for _, loc := range ahEpisodeRe.FindAllStringSubmatchIndex(page, -1) {
		key := page[loc[2]:loc[3]]
		// The number must sit inside this anchor: stop at its close or at the
		// next anchor, so an unclosed one cannot borrow a neighbour's cell.
		seg := page[loc[1]:]
		if i := strings.Index(seg, "</a>"); i >= 0 {
			seg = seg[:i]
		}
		if i := strings.Index(seg, "<a "); i >= 0 {
			seg = seg[:i]
		}
		nm := ahNumberRe.FindStringSubmatch(seg)
		if nm == nil {
			continue
		}
		n, err := strconv.Atoi(nm[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, media.Episode{Number: n, ID: key, Title: fmt.Sprintf("Episode %d", n)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// GetServers returns the one source the site offers.
func (a *AnimeHeaven) GetServers(id string, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AnimeHeaven", ID: episodeID}}, nil
}

// Watch resolves an episode to a direct mp4 URL. episodeID is a gate key from
// GetEpisodes, or the fallback resolver's form ("" or "show:season:episode"),
// which is looked up in the episode list first. It returns the first https
// <source> outside an HTML comment that is not the page's error fallback; the
// site lists mirrors in that order.
func (a *AnimeHeaven) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	key := episodeID
	if !ahKeyRe.MatchString(key) {
		var err error
		if key, err = resolveNumericEpisodeID(a.GetEpisodes, mediaID, episodeID); err != nil {
			return nil, fmt.Errorf("animeheaven: %w", err)
		}
		if !ahKeyRe.MatchString(key) {
			return nil, fmt.Errorf("animeheaven: invalid episode key")
		}
	}
	page, err := a.get(a.root()+"/gate.php", key)
	if err != nil {
		return nil, err
	}
	page = ahCommentRe.ReplaceAllString(page, "")
	for _, m := range ahSourceRe.FindAllStringSubmatch(page, -1) {
		src := strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				return -1
			}
			return r
		}, html.UnescapeString(m[1]))
		if strings.HasSuffix(src, "&error") || strings.HasSuffix(src, "&error2") {
			continue
		}
		if u, err := url.Parse(src); err != nil || u.Scheme != "https" || u.Host == "" {
			continue
		}
		return &media.Stream{URL: src}, nil
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
