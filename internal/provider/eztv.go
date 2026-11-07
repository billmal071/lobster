package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// eztvBase is the EZTV public JSON API. A var so tests can point it at a stub.
// eztv.re answers with a 301 to eztvx.to; eztv.wf serves the same API.
var eztvBase = "https://eztvx.to"

// eztvPageSize is the API maximum.
const eztvPageSize = 100

// eztvMaxPages bounds a show's torrent list (1000 releases) so a runaway count cannot page forever.
const eztvMaxPages = 10

// eztvSearchLimit caps the TMDB hits resolved to IMDb per search; each costs a page fetch.
const eztvSearchLimit = 5

// eztvCacheTTL is how long a show's torrent list is reused across seasons → episodes → servers → watch.
const eztvCacheTTL = 5 * time.Minute

// EZTV resolves TV episodes to magnets from the EZTV public API, which is
// keyed by IMDb id: Search goes through TMDB's keyless multi-search and reads
// each hit's IMDb id off its TMDB page.
type EZTV struct {
	client      *http.Client
	tmdbBaseURL string

	mu    sync.Mutex
	shows map[string]eztvCachedShow // imdb digits -> torrents
	imdb  map[int]string            // tmdb tv id -> imdb digits ("" = none)
}

type eztvCachedShow struct {
	torrents []eztvTorrent
	fetched  time.Time
}

func NewEZTV() *EZTV {
	return &EZTV{
		client:      httputil.NewClient(),
		tmdbBaseURL: tmdbSearchBase,
		shows:       map[string]eztvCachedShow{},
		imdb:        map[int]string{},
	}
}

// eztvInt decodes a field EZTV serves as either a number or a numeric string.
type eztvInt int64

func (n *eztvInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("eztv: bad integer %s", string(b))
	}
	*n = eztvInt(v)
	return nil
}

type eztvTorrent struct {
	ID        int     `json:"id"`
	Hash      string  `json:"hash"`
	Filename  string  `json:"filename"`
	Title     string  `json:"title"`
	MagnetURL string  `json:"magnet_url"`
	Season    eztvInt `json:"season"`
	Episode   eztvInt `json:"episode"`
	Seeds     eztvInt `json:"seeds"`
	Peers     eztvInt `json:"peers"`
	SizeBytes eztvInt `json:"size_bytes"`
	Released  eztvInt `json:"date_released_unix"`
}

// releaseName is the name quality and tags are parsed from; season packs have no filename, so the title stands in.
func (t eztvTorrent) releaseName() string {
	if strings.TrimSpace(t.Filename) != "" {
		return t.Filename
	}
	return t.Title
}

type eztvResponse struct {
	TorrentsCount int           `json:"torrents_count"`
	Torrents      []eztvTorrent `json:"torrents"`
}

func (e *EZTV) fetchPage(imdb string, page int) (*eztvResponse, error) {
	apiURL := fmt.Sprintf("%s/api/get-torrents?imdb_id=%s&limit=%d&page=%d",
		strings.TrimRight(eztvBase, "/"), imdb, eztvPageSize, page)
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eztv request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eztv: unexpected status %d", resp.StatusCode)
	}
	var out eztvResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("parsing eztv response: %w", err)
	}
	return &out, nil
}

// eztvIMDB extracts the IMDb digits from a provider ID like "eztv/0903747".
func eztvIMDB(id string) (string, error) {
	digits := strings.TrimPrefix(strings.TrimPrefix(id, "eztv/"), "tt")
	if err := httputil.ValidateNumericID(digits); err != nil {
		return "", fmt.Errorf("eztv: invalid show ID %q", id)
	}
	return digits, nil
}

// torrents returns every release EZTV lists for a show, cached per show so the four-step sequence costs one fetch.
func (e *EZTV) torrents(id string) ([]eztvTorrent, error) {
	imdb, err := eztvIMDB(id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	cached, ok := e.shows[imdb]
	e.mu.Unlock()
	if ok && time.Since(cached.fetched) < eztvCacheTTL {
		return cached.torrents, nil
	}

	var all []eztvTorrent
	for page := 1; page <= eztvMaxPages; page++ {
		resp, err := e.fetchPage(imdb, page)
		if err != nil {
			// Keep earlier pages: a partial catalogue beats failing outright
			// because page 3 timed out.
			if len(all) > 0 {
				break
			}
			return nil, err
		}
		all = append(all, resp.Torrents...)
		// A page past the end carries no torrents key at all, so a short or
		// empty page ends the walk as surely as reaching the count does.
		if len(resp.Torrents) < eztvPageSize || len(all) >= resp.TorrentsCount {
			break
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("eztv: no torrents for %s", id)
	}
	e.mu.Lock()
	e.shows[imdb] = eztvCachedShow{torrents: all, fetched: time.Now()}
	e.mu.Unlock()
	return all, nil
}

// --- Search: TMDB title → IMDb id ---

// Search finds TV shows by title on TMDB and keeps the hits that have an IMDb id.
func (e *EZTV) Search(query string) ([]media.SearchResult, error) {
	hits, err := e.tmdbTVSearch(query)
	if err != nil {
		return nil, fmt.Errorf("eztv search: %w", err)
	}
	if len(hits) > eztvSearchLimit {
		hits = hits[:eztvSearchLimit]
	}
	// Resolve IMDb ids concurrently: each is a full TMDB page fetch, and the
	// search should not take five times as long as one.
	ids := make([]string, len(hits))
	var wg sync.WaitGroup
	for i := range hits {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = e.imdbFor(hits[i].ID)
		}(i)
	}
	wg.Wait()

	var results []media.SearchResult
	for i, h := range hits {
		if ids[i] == "" {
			continue
		}
		results = append(results, media.SearchResult{
			ID:     "eztv/" + ids[i],
			Title:  h.displayTitle(),
			Year:   h.year(),
			Type:   media.TV,
			Poster: tmdbPosterURL(h.PosterPath),
		})
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%w for %q", ErrNoResults, query)
	}
	return results, nil
}

func (e *EZTV) tmdbGet(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", strings.TrimRight(e.tmdbBaseURL, "/")+"/")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb: unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

// tmdbTVSearch returns the TV entries of a TMDB multi-search, in TMDB's order.
func (e *EZTV) tmdbTVSearch(query string) ([]tmdbSearchResult, error) {
	body, err := e.tmdbGet(tmdbMultiSearchURL(e.tmdbBaseURL, query))
	if err != nil {
		return nil, err
	}
	var sr tmdbSearchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("parsing tmdb response: %w", err)
	}
	var hits []tmdbSearchResult
	for _, raw := range sr.Results {
		if len(raw) == 0 || raw[0] != '{' { // autocomplete <span> strings
			continue
		}
		var item tmdbSearchResult
		if json.Unmarshal(raw, &item) != nil || item.MediaType != "tv" {
			continue
		}
		hits = append(hits, item)
	}
	return hits, nil
}

// tmdbIMDBLink matches the IMDb link on a TMDB show page; the keyless site has no external_ids endpoint.
var tmdbIMDBLink = regexp.MustCompile(`imdb\.com/title/tt(\d+)`)

// imdbFor resolves a TMDB TV id to IMDb digits ("" when none), memoised per instance.
func (e *EZTV) imdbFor(tmdbID int) string {
	e.mu.Lock()
	v, ok := e.imdb[tmdbID]
	e.mu.Unlock()
	if ok {
		return v
	}
	body, err := e.tmdbGet(fmt.Sprintf("%s/tv/%d", strings.TrimRight(e.tmdbBaseURL, "/"), tmdbID))
	if err != nil {
		return "" // transient: not memoised, so the next search retries
	}
	id := ""
	if m := tmdbIMDBLink.FindSubmatch(body); m != nil {
		id = string(m[1])
	}
	e.mu.Lock()
	e.imdb[tmdbID] = id
	e.mu.Unlock()
	return id
}

// --- Seasons and episodes ---

func (e *EZTV) GetSeasons(id string) ([]media.Season, error) {
	torrents, err := e.torrents(id)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var nums []int
	for _, t := range torrents {
		s := int(t.Season)
		if s <= 0 || t.Episode <= 0 || seen[s] {
			continue
		}
		seen[s] = true
		nums = append(nums, s)
	}
	sort.Ints(nums)
	seasons := make([]media.Season, 0, len(nums))
	for _, n := range nums {
		seasons = append(seasons, media.Season{Number: n, ID: strconv.Itoa(n)})
	}
	return seasons, nil
}

// GetEpisodes lists the episodes of a season with at least one release; season packs (episode 0) are left out.
func (e *EZTV) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	season, err := strconv.Atoi(strings.TrimSpace(seasonID))
	if err != nil {
		return nil, fmt.Errorf("eztv: invalid season ID %q", seasonID)
	}
	torrents, err := e.torrents(id)
	if err != nil {
		return nil, err
	}
	titles := map[int]string{}
	seen := map[int]bool{}
	var nums []int
	for _, t := range torrents {
		if int(t.Season) != season || t.Episode <= 0 {
			continue
		}
		n := int(t.Episode)
		if !seen[n] {
			seen[n] = true
			nums = append(nums, n)
		}
		// Keep the longest title seen: a release that drops the episode
		// name should not win over one that carries it.
		if title := eztvEpisodeTitle(t.Title); len(title) > len(titles[n]) {
			titles[n] = title
		}
	}
	sort.Ints(nums)
	eps := make([]media.Episode, 0, len(nums))
	for _, n := range nums {
		title := titles[n]
		if title == "" {
			title = fmt.Sprintf("Episode %d", n)
		}
		eps = append(eps, media.Episode{Number: n, Title: title, ID: fmt.Sprintf("%dx%d", season, n)})
	}
	return eps, nil
}

// eztvEpisodeMarker finds the SxxEyy token and what follows it.
var eztvEpisodeMarker = regexp.MustCompile(`(?i)\bS\d{1,3}E\d{1,3}\b[ ._-]*(.*)$`)

// eztvReleaseTag matches the first release tag after the episode marker, where the episode name ends.
var eztvReleaseTag = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|360p|4K|UHD|HDTV|WEB[ .-]?(DL|Rip)|BluRay|BDRip|DVDRip|HDRip|x264|x265|h\.?264|h\.?265|HEVC|XviD|AAC|DDP?\d|AC3|EAC3|PROPER|REPACK|iNTERNAL|AMZN|ATVP|DSNP|HMAX|HULU|PCOK|MULTi|REMUX|EZTV)\b`)

// eztvBareWeb is the bare "WEB" tag, matched case-sensitively so "The Web Of Lies" keeps its name.
var eztvBareWeb = regexp.MustCompile(`\bWEB\b`)

// eztvEpisodeTitle pulls the episode name out of a release title ("" when it carries none).
func eztvEpisodeTitle(release string) string {
	m := eztvEpisodeMarker.FindStringSubmatch(release)
	if m == nil {
		return ""
	}
	rest := strings.NewReplacer(".", " ", "_", " ").Replace(m[1])
	if loc := eztvReleaseTag.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	if loc := eztvBareWeb.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	rest = strings.TrimSpace(strings.Trim(rest, " -"))
	if rest == "" {
		return ""
	}
	return strings.Join(strings.Fields(rest), " ")
}

// --- Servers and quality ---

var eztvQualityToken = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|360p|4K|UHD|HDTV)\b`)

// eztvQualityHeight reads the vertical resolution from a release name: bare HDTV is 480, none is 0.
func eztvQualityHeight(release string) int {
	m := eztvQualityToken.FindStringSubmatch(release)
	if m == nil {
		return 0
	}
	switch strings.ToLower(m[1]) {
	case "4k", "uhd":
		return 2160
	case "hdtv":
		return 480
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(m[1]), "p"))
	return n
}

// eztvQualityLabel is the "<height>p" token that leads a server name, which the quality preference matches on.
func eztvQualityLabel(release string) string {
	if h := eztvQualityHeight(release); h > 0 {
		return strconv.Itoa(h) + "p"
	}
	return "SD"
}

// eztvSplitCodec matches a codec written with a separator ("H 264") so it can be rejoined before tagging.
var eztvSplitCodec = regexp.MustCompile(`(?i)\b([hx])[ .](26[45])\b`)

// eztvGroup is the release's codec and group ("x265-MeGusta"): its last tag, minus EZTV's suffix.
func eztvGroup(release string) string {
	name := release
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".mkv"), ".mp4"), ".avi")
	name = eztvSplitCodec.ReplaceAllString(name, "$1$2")
	fields := strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(name))
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if strings.EqualFold(f, "EZTV") || eztvQualityToken.MatchString(f) {
			continue
		}
		return f
	}
	return ""
}

// eztvSize renders size_bytes the way YTS reports sizes ("2.00 GB", "899 MB").
func eztvSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%d MB", bytes/(1<<20))
	case bytes > 0:
		return fmt.Sprintf("%d KB", bytes/(1<<10))
	}
	return "? MB"
}

// eztvServerName is the name a user picks a torrent by: quality first, then seeds.
func eztvServerName(t eztvTorrent) string {
	name := t.releaseName()
	return fmt.Sprintf("%s %s (%s, %d seeds)", eztvQualityLabel(name), eztvGroup(name), eztvSize(int64(t.SizeBytes)), t.Seeds)
}

// eztvEpisodeRef parses an episode ID: native "2x10" or the resolver's
// "showID:season:episode". "" is a movie request, which EZTV cannot serve.
func eztvEpisodeRef(episodeID string) (season, episode int, err error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return 0, 0, fmt.Errorf("eztv: TV only, no episode requested")
	}
	parts := strings.FieldsFunc(episodeID, func(r rune) bool { return r == 'x' || r == 'X' || r == ':' })
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("eztv: bad episode id %q", episodeID)
	}
	s, err1 := strconv.Atoi(parts[len(parts)-2])
	ep, err2 := strconv.Atoi(parts[len(parts)-1])
	if err1 != nil || err2 != nil || s <= 0 || ep <= 0 {
		return 0, 0, fmt.Errorf("eztv: bad episode id %q", episodeID)
	}
	return s, ep, nil
}

// episodeTorrents returns an episode's releases, best quality then most seeded; unseeded ones only when nothing else exists.
func (e *EZTV) episodeTorrents(id, episodeID string) ([]eztvTorrent, error) {
	season, episode, err := eztvEpisodeRef(episodeID)
	if err != nil {
		return nil, err
	}
	all, err := e.torrents(id)
	if err != nil {
		return nil, err
	}
	var out []eztvTorrent
	for _, t := range all {
		if int(t.Season) == season && int(t.Episode) == episode && t.MagnetURL != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("eztv: no torrents for %s S%02dE%02d", id, season, episode)
	}
	return sortedEZTVTorrents(out), nil
}

func sortedEZTVTorrents(in []eztvTorrent) []eztvTorrent {
	seeded := make([]eztvTorrent, 0, len(in))
	for _, t := range in {
		if t.Seeds > 0 {
			seeded = append(seeded, t)
		}
	}
	out := in
	if len(seeded) > 0 {
		out = seeded
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, hj := eztvQualityHeight(out[i].releaseName()), eztvQualityHeight(out[j].releaseName())
		if hi != hj {
			return hi > hj
		}
		return out[i].Seeds > out[j].Seeds
	})
	return out
}

func (e *EZTV) GetServers(id string, episodeID string) ([]media.Server, error) {
	torrents, err := e.episodeTorrents(id, episodeID)
	if err != nil {
		return nil, err
	}
	servers := make([]media.Server, 0, len(torrents))
	for i, t := range torrents {
		servers = append(servers, media.Server{Name: eztvServerName(t), ID: strconv.Itoa(i)})
	}
	return servers, nil
}

// pickEZTVTorrent chooses by server name or index, then the requested quality,
// then the highest known quality below it, then the best available.
func pickEZTVTorrent(torrents []eztvTorrent, server, quality string) eztvTorrent {
	for i, t := range torrents {
		if server != "" && (strings.EqualFold(server, eztvServerName(t)) || server == strconv.Itoa(i)) {
			return t
		}
	}
	if want := strings.ToLower(strings.TrimSpace(quality)); want != "" && want != "best" {
		if h, err := strconv.Atoi(strings.TrimSuffix(want, "p")); err == nil {
			lower, lowerH := -1, 0
			for i, t := range torrents {
				th := eztvQualityHeight(t.releaseName())
				if th == h {
					return t
				}
				if th < h && th > lowerH {
					lower, lowerH = i, th
				}
			}
			if lower >= 0 {
				return torrents[lower]
			}
		}
	}
	return torrents[0]
}

// Watch returns the release's magnet URI as-is (EZTV's magnets already carry trackers).
func (e *EZTV) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	torrents, err := e.episodeTorrents(mediaID, episodeID)
	if err != nil {
		return nil, err
	}
	t := pickEZTVTorrent(torrents, server, quality)
	return &media.Stream{
		URL:     t.MagnetURL,
		Quality: eztvQualityLabel(t.releaseName()),
	}, nil
}

// GetDetails has nothing cheap to offer: EZTV carries releases, not metadata.
func (e *EZTV) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

func (e *EZTV) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("eztv: use Watch instead")
}

// EZTV's feeds are per-release, not per-show, so there is no browse view.
func (e *EZTV) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func (e *EZTV) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
