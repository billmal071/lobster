package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/titlekey"
)

// x1337Bases are the 1337x mirrors, tried in order from the last one that answered.
var x1337Bases = []string{"https://1337x.to", "https://1337xx.to", "https://x1337x.eu"}

const x1337MaxPages = 3

const (
	x1337MaxPageBytes   = 5 << 20
	x1337MaxRows        = 200
	x1337MaxQueryLen    = 200
	x1337MaxDetailTries = 5
	x1337CacheTTL       = 5 * time.Minute
	x1337CacheMax       = 32
	x1337UserAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"
)

var (
	x1337RequestTimeout = 10 * time.Second
	x1337Budget         = 30 * time.Second
)

var x1337Trackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:451/announce",
}

var x1337Categories = []string{"Movies", "TV"}

var (
	x1337TorrentPath = regexp.MustCompile(`^/torrent/(\d{1,12})/[A-Za-z0-9._%~+-]{1,200}/$`)
	x1337MagnetRe    = regexp.MustCompile(`^magnet:\?xt=urn:btih:([0-9A-Fa-f]{40}|[A-Za-z2-7]{32})(?:&|$)`)
	x1337SizeRe      = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(GB|MB|KB)$`)
	x1337SpaceRe     = regexp.MustCompile(`\s+`)
)

// X1337 resolves movies and series to magnets scraped from 1337x. A search page lists releases; a release's detail page carries its magnet.
type X1337 struct {
	client *http.Client

	mu    sync.Mutex
	good  int
	cache map[string]x1337CacheEntry
}

type x1337CacheEntry struct {
	rows []x1337Row
	at   time.Time
}

func NewX1337() *X1337 {
	return &X1337{client: httputil.NewClient(), cache: map[string]x1337CacheEntry{}}
}

type x1337Row struct {
	ID      int
	Path    string
	Name    string
	Seeds   int
	Size    int64
	Season  int
	Episode int
	Year    string
}

// fetch tries each mirror starting from the last that answered and returns the first page recognised accepts.
func (x *X1337) fetch(ctx context.Context, path string, recognised func(*goquery.Document) bool) (*goquery.Document, error) {
	n := len(x1337Bases)
	if n == 0 {
		return nil, errors.New("1337x: no mirrors configured")
	}
	x.mu.Lock()
	start := x.good
	x.mu.Unlock()
	var errs []error
	for i := 0; i < n && ctx.Err() == nil; i++ {
		idx := (start + i) % n
		doc, err := x.fetchFrom(ctx, x1337Bases[idx], path, recognised)
		if err == nil {
			x.mu.Lock()
			x.good = idx
			x.mu.Unlock()
			return doc, nil
		}
		errs = append(errs, err)
	}
	if err := ctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("1337x: gave up after %v: %w", x1337Budget, err))
	}
	return nil, errors.Join(errs...)
}

func (x *X1337) fetchFrom(parent context.Context, base, path string, recognised func(*goquery.Document) bool) (*goquery.Document, error) {
	ctx, cancel := context.WithTimeout(parent, x1337RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", x1337UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	resp, err := x.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("1337x request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("1337x: %s answered status %d", base, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, x1337MaxPageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading 1337x page: %w", err)
	}
	if len(body) > x1337MaxPageBytes {
		return nil, fmt.Errorf("1337x: %s page exceeds the %d MiB size limit", base, x1337MaxPageBytes>>20)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil || !recognised(doc) {
		return nil, fmt.Errorf("%w: %s", ErrUnrecognisedResponse, base)
	}
	return doc, nil
}

func x1337IsSearchPage(doc *goquery.Document) bool {
	return doc.Find("table.table-list").Length() > 0 ||
		strings.Contains(doc.Find("body").Text(), "No results were returned")
}

func x1337IsDetailPage(doc *goquery.Document) bool {
	return doc.Find("div.torrent-detail-page").Length() > 0 || doc.Find(`a[href^="magnet:?"]`).Length() > 0
}

func x1337Size(s string) int64 {
	m := x1337SizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch m[2] {
	case "GB":
		return int64(v * (1 << 30))
	case "MB":
		return int64(v * (1 << 20))
	}
	return int64(v * (1 << 10))
}

func x1337ParseRows(doc *goquery.Document) []x1337Row {
	var rows []x1337Row
	seen := map[string]bool{}
	doc.Find("table.table-list tbody tr").EachWithBreak(func(_ int, tr *goquery.Selection) bool {
		a := tr.Find(`td.name a[href^="/torrent/"]`).First()
		href, _ := a.Attr("href")
		m := x1337TorrentPath.FindStringSubmatch(href)
		name := strings.TrimSpace(a.Text())
		if m == nil || name == "" || seen[href] {
			return true
		}
		seen[href] = true
		id, _ := strconv.Atoi(m[1])
		seeds, _ := strconv.Atoi(strings.TrimSpace(tr.Find("td.seeds").First().Text()))
		row := x1337Row{
			ID: id, Path: href, Name: name, Seeds: seeds,
			Size: x1337Size(tr.Find("td.size").First().Contents().First().Text()),
		}
		row.Season, row.Episode = x1337SeasonEpisode(name)
		rows = append(rows, row)
		return len(rows) < x1337MaxRows
	})
	return rows
}

func x1337NormaliseQuery(q string) string {
	return strings.TrimSpace(x1337SpaceRe.ReplaceAllString(q, " "))
}

// rows searches each category up to x1337MaxPages pages and keeps the rows whose title matches the query.
// A category or page that fails leaves the rest in use, uncached; every first page failing is an error.
func (x *X1337) rows(ctx context.Context, query string) ([]x1337Row, error) {
	x.mu.Lock()
	if e, ok := x.cache[query]; ok && time.Since(e.at) < x1337CacheTTL {
		x.mu.Unlock()
		return e.rows, nil
	}
	x.mu.Unlock()
	var rows []x1337Row
	var errs []error
	seen := map[string]bool{}
	complete, reached := true, false
	for _, category := range x1337Categories {
		for page := 1; page <= x1337MaxPages && len(rows) < x1337MaxRows; page++ {
			doc, err := x.fetch(ctx, fmt.Sprintf("/category-search/%s/%s/%d/", url.PathEscape(query), category, page), x1337IsSearchPage)
			if err != nil {
				errs = append(errs, err)
				complete = false
				break
			}
			reached = true
			for _, r := range x1337ParseRows(doc) {
				year, ok := x1337MatchRelease(query, r.Name)
				if ok && !seen[r.Path] && len(rows) < x1337MaxRows {
					seen[r.Path] = true
					r.Year = year
					rows = append(rows, r)
				}
			}
			if !x1337HasNextPage(doc, page+1) {
				break
			}
		}
	}
	if !reached || (len(rows) == 0 && len(errs) > 0) {
		return nil, errors.Join(errs...)
	}
	if complete {
		x.mu.Lock()
		if len(x.cache) >= x1337CacheMax {
			x.cache = map[string]x1337CacheEntry{}
		}
		x.cache[query] = x1337CacheEntry{rows: rows, at: time.Now()}
		x.mu.Unlock()
	}
	return rows, nil
}

func x1337HasNextPage(doc *goquery.Document, next int) bool {
	suffix := fmt.Sprintf("/%d/", next)
	found := false
	doc.Find(".pagination a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		found = strings.HasPrefix(href, "/category-search/") && strings.HasSuffix(href, suffix)
		return !found
	})
	return found
}

func x1337SeasonEpisode(name string) (season, episode int) {
	if e := nyaaSxxExxRe.FindStringSubmatch(name); e != nil {
		season, _ = strconv.Atoi(e[1])
		episode, _ = strconv.Atoi(e[2])
		return season, episode
	}
	for _, re := range []*regexp.Regexp{nyaaSeasonWordRe, x1337SeasonOnlyRe} {
		if m := re.FindStringSubmatch(name); m != nil {
			season, _ = strconv.Atoi(m[1])
			return season, 0
		}
	}
	return 0, 0
}

func x1337IsSeries(query string, rows []x1337Row) bool {
	if s, _ := x1337SeasonEpisode(query); s > 0 {
		return true
	}
	seasons := 0
	for _, r := range rows {
		if r.Season > 0 {
			seasons++
		}
	}
	return seasons*2 > len(rows)
}

var (
	x1337YearToken    = regexp.MustCompile(`^(?:19|20)\d{2}$`)
	x1337MarkerToken  = regexp.MustCompile(`^(?:s\d{2}|s\d{1,2}e\d{1,4}(?:v\d+)?)$`)
	x1337SeasonNumber = regexp.MustCompile(`^\d{1,2}$`)
	x1337SeasonOnlyRe = regexp.MustCompile(`(?i)\bS(\d{2})\b`)
	x1337TagTokens    = map[string]bool{"web": true, "hdr": true, "imax": true, "hdcam": true, "brrip": true, "nf": true, "remastered": true, "extended": true}
)

func x1337PlausibleYear(token string) bool {
	if !x1337YearToken.MatchString(token) {
		return false
	}
	year, _ := strconv.Atoi(token)
	return year <= time.Now().Year()+1
}

// x1337QueryTitle is the query up to its season or episode marker.
func x1337QueryTitle(query string) string {
	cut := len(query)
	for _, re := range []*regexp.Regexp{nyaaSxxExxRe, nyaaSeasonWordRe, x1337SeasonOnlyRe} {
		if loc := re.FindStringIndex(query); loc != nil && loc[0] < cut {
			cut = loc[0]
		}
	}
	return query[:cut]
}

// x1337MatchRelease decides whether a release name is the work the query names, and returns its release year.
// The name's words must start with the query's (compared as one run, so "Spiderman" meets "Spider-Man"). What follows decides:
// the end of the name, a release year, a season or episode marker, or a release tag. Anything else is another title
// ("Lost" against "Lost in Space"), and whatever comes after a year is ignored. A number right after the title only counts
// as a year when it is one a release can have (up to next year), so "Blade Runner" does not meet "Blade Runner 2049".
// A year ending the query itself is the year the release is for.
func x1337MatchRelease(query, name string) (string, bool) {
	q := strings.Fields(titlekey.Normalize(x1337QueryTitle(query)))
	n := strings.Fields(titlekey.Normalize(name))
	want := strings.Join(q, "")
	if want == "" {
		return "", false
	}
	got, i := "", 0
	for i < len(n) && len(got) < len(want) {
		got += n[i]
		i++
	}
	if got != want {
		return "", false
	}
	year := ""
	if len(q) > 1 && x1337PlausibleYear(q[len(q)-1]) {
		year = q[len(q)-1]
	}
	rest := n[i:]
	if len(rest) == 0 {
		return year, true
	}
	t := rest[0]
	switch {
	case x1337PlausibleYear(t):
		return t, true
	case x1337MarkerToken.MatchString(t), x1337TagTokens[t], eztvReleaseTag.MatchString(t):
		return year, true
	case t == "season" && len(rest) > 1 && x1337SeasonNumber.MatchString(rest[1]):
		return year, true
	}
	return "", false
}

func x1337QueryFromID(id string) (query, year string, err error) {
	rest, ok := strings.CutPrefix(id, "1337x/")
	if !ok {
		return "", "", fmt.Errorf("1337x: invalid ID %q", id)
	}
	rest, year, hasYear := strings.Cut(rest, "?y=")
	q, err := url.QueryUnescape(rest)
	q = x1337NormaliseQuery(q)
	if err != nil || q == "" || len(q) > x1337MaxQueryLen || (hasYear && !x1337YearToken.MatchString(year)) {
		return "", "", fmt.Errorf("1337x: invalid ID %q", id)
	}
	return q, year, nil
}

func x1337Context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), x1337Budget)
}

func (x *X1337) catalogue(ctx context.Context, id string) (string, []x1337Row, error) {
	q, year, err := x1337QueryFromID(id)
	if err != nil {
		return "", nil, err
	}
	rows, err := x.rows(ctx, q)
	if err != nil {
		return "", nil, err
	}
	if year != "" {
		var kept []x1337Row
		for _, r := range rows {
			if r.Year == year {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if len(rows) == 0 {
		return "", nil, fmt.Errorf("1337x: no releases for %q: %w", q, ErrNoResults)
	}
	return q, rows, nil
}

// Search returns one result for the query: 1337x is a flat list of releases, so seasons, episodes and qualities are read from the same page later.
func (x *X1337) Search(query string) ([]media.SearchResult, error) {
	q := x1337NormaliseQuery(query)
	if q == "" {
		return nil, errors.New("1337x: empty query")
	}
	if len(q) > x1337MaxQueryLen {
		return nil, fmt.Errorf("1337x: query longer than %d characters", x1337MaxQueryLen)
	}
	ctx, cancel := x1337Context()
	defer cancel()
	rows, err := x.rows(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("1337x search: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w for %q", ErrNoResults, q)
	}
	id := "1337x/" + url.QueryEscape(q)
	if x1337IsSeries(q, rows) {
		return []media.SearchResult{{ID: id, Title: q, Type: media.TV}}, nil
	}
	seeds := map[string]int{}
	var years []string
	for _, r := range rows {
		y := r.Year
		if y == "" {
			continue
		}
		if _, ok := seeds[y]; !ok {
			years = append(years, y)
		}
		seeds[y] += r.Seeds
	}
	if len(years) < 2 {
		return []media.SearchResult{{ID: id, Title: q, Type: media.Movie}}, nil
	}
	sort.SliceStable(years, func(i, j int) bool { return seeds[years[i]] > seeds[years[j]] })
	results := make([]media.SearchResult, 0, len(years))
	for _, y := range years {
		results = append(results, media.SearchResult{ID: id + "?y=" + y, Title: q, Year: y, Type: media.Movie})
	}
	return results, nil
}

func (x *X1337) GetSeasons(id string) ([]media.Season, error) {
	ctx, cancel := x1337Context()
	defer cancel()
	q, rows, err := x.catalogue(ctx, id)
	if err != nil {
		return nil, err
	}
	if !x1337IsSeries(q, rows) {
		return nil, nil
	}
	seen := map[int]bool{}
	var nums []int
	for _, r := range rows {
		if r.Season > 0 && r.Episode > 0 && !seen[r.Season] {
			seen[r.Season] = true
			nums = append(nums, r.Season)
		}
	}
	sort.Ints(nums)
	seasons := make([]media.Season, 0, len(nums))
	for _, n := range nums {
		seasons = append(seasons, media.Season{Number: n, ID: strconv.Itoa(n)})
	}
	return seasons, nil
}

func (x *X1337) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	season, err := strconv.Atoi(strings.TrimSpace(seasonID))
	if err != nil {
		return nil, fmt.Errorf("1337x: invalid season ID %q", seasonID)
	}
	ctx, cancel := x1337Context()
	defer cancel()
	_, rows, err := x.catalogue(ctx, id)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var nums []int
	for _, r := range rows {
		if r.Season == season && r.Episode > 0 && !seen[r.Episode] {
			seen[r.Episode] = true
			nums = append(nums, r.Episode)
		}
	}
	sort.Ints(nums)
	eps := make([]media.Episode, 0, len(nums))
	for _, n := range nums {
		eps = append(eps, media.Episode{Number: n, Title: fmt.Sprintf("Episode %d", n), ID: fmt.Sprintf("%dx%d", season, n)})
	}
	return eps, nil
}

// x1337Candidate is one release for the requested episode (or film), with its detail path.
type x1337Candidate struct {
	torrent eztvTorrent
	path    string
}

// candidates returns every release of the request, ordered like EZTV's (quality, then seeds), and the best-seeded one of each quality.
// A series with no episode in the request takes it from the query's SxxEyy marker; without one it is refused.
func (x *X1337) candidates(ctx context.Context, id, episodeID string) (all, perQuality []x1337Candidate, err error) {
	q, rows, err := x.catalogue(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	series := x1337IsSeries(q, rows)
	season, episode := 0, 0
	switch {
	case strings.TrimSpace(episodeID) != "":
		if season, episode, err = eztvEpisodeRef(episodeID); err != nil {
			return nil, nil, err
		}
	case series:
		m := nyaaSxxExxRe.FindStringSubmatch(q)
		if m == nil {
			return nil, nil, fmt.Errorf("1337x: %q is a series, pick an episode", q)
		}
		season, _ = strconv.Atoi(m[1])
		episode, _ = strconv.Atoi(m[2])
	}

	var torrents []eztvTorrent
	paths := map[int]string{}
	for _, r := range rows {
		if r.Season != season || r.Episode != episode {
			continue
		}
		torrents = append(torrents, eztvTorrent{
			ID: r.ID, Title: r.Name, Filename: r.Name, Season: eztvInt(r.Season), Episode: eztvInt(r.Episode),
			Seeds: eztvInt(r.Seeds), SizeBytes: eztvInt(r.Size),
		})
		paths[r.ID] = r.Path
	}
	if len(torrents) == 0 {
		return nil, nil, fmt.Errorf("1337x: no releases for %q S%02dE%02d: %w", q, season, episode, ErrNoResults)
	}
	sort.Slice(torrents, func(i, j int) bool { return torrents[i].ID < torrents[j].ID })
	seenHeight := map[int]bool{}
	for _, t := range sortedEZTVTorrents(torrents) {
		c := x1337Candidate{torrent: t, path: paths[t.ID]}
		all = append(all, c)
		if h := eztvQualityHeight(t.releaseName()); !seenHeight[h] {
			seenHeight[h] = true
			perQuality = append(perQuality, c)
		}
	}
	return all, perQuality, nil
}

func x1337ServerID(torrentID int) string { return "r" + strconv.Itoa(torrentID) }

func (x *X1337) GetServers(id string, episodeID string) ([]media.Server, error) {
	ctx, cancel := x1337Context()
	defer cancel()
	_, best, err := x.candidates(ctx, id, episodeID)
	if err != nil {
		return nil, err
	}
	servers := make([]media.Server, 0, len(best))
	for _, c := range best {
		servers = append(servers, media.Server{Name: eztvServerName(c.torrent), ID: x1337ServerID(c.torrent.ID)})
	}
	return servers, nil
}

func x1337MagnetHash(magnet string) (string, bool) {
	if len(magnet) > 4096 {
		return "", false
	}
	m := x1337MagnetRe.FindStringSubmatch(magnet)
	if m == nil {
		return "", false
	}
	if len(m[1]) == 32 {
		return strings.ToUpper(m[1]), true
	}
	return m[1], true
}

// infoHash reads a release's info hash from its detail page; "" when the page carries no valid magnet.
func (x *X1337) infoHash(ctx context.Context, path string) (string, error) {
	doc, err := x.fetch(ctx, path, x1337IsDetailPage)
	if err != nil {
		return "", err
	}
	found := ""
	doc.Find(`a[href^="magnet:?"]`).EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, _ := a.Attr("href")
		if hash, ok := x1337MagnetHash(href); ok {
			found = hash
			return false
		}
		return true
	})
	return found, nil
}

func x1337Magnet(hash, title string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(hash)
	b.WriteString("&dn=")
	b.WriteString(url.QueryEscape(title))
	for _, tr := range x1337Trackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

// Watch resolves a release's magnet. The release is the one the server ID names, else the pick for the quality; a release whose
// detail page has no magnet is passed over for the other releases of its quality by seeds, then for the other qualities.
func (x *X1337) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	ctx, cancel := x1337Context()
	defer cancel()
	all, best, err := x.candidates(ctx, mediaID, episodeID)
	if err != nil {
		return nil, err
	}
	var first *x1337Candidate
	for i := range all {
		if server != "" && server == x1337ServerID(all[i].torrent.ID) {
			first = &all[i]
			break
		}
	}
	if first == nil {
		torrents := make([]eztvTorrent, len(best))
		for i, c := range best {
			torrents[i] = c.torrent
		}
		picked := pickEZTVTorrent(torrents, server, quality)
		for i := range all {
			if all[i].torrent.ID == picked.ID {
				first = &all[i]
			}
		}
	}
	height := eztvQualityHeight(first.torrent.releaseName())
	ordered := []x1337Candidate{*first}
	for _, c := range all {
		if c.torrent.ID != first.torrent.ID && eztvQualityHeight(c.torrent.releaseName()) == height {
			ordered = append(ordered, c)
		}
	}
	for _, c := range all {
		if eztvQualityHeight(c.torrent.releaseName()) != height {
			ordered = append(ordered, c)
		}
	}
	if len(ordered) > x1337MaxDetailTries {
		ordered = ordered[:x1337MaxDetailTries]
	}
	var lastErr error
	for _, c := range ordered {
		if ctx.Err() != nil {
			break
		}
		hash, err := x.infoHash(ctx, c.path)
		if err != nil {
			lastErr = err
			continue
		}
		if hash != "" {
			name := c.torrent.releaseName()
			return &media.Stream{URL: x1337Magnet(hash, name), Quality: eztvQualityLabel(name)}, nil
		}
	}
	if lastErr == nil && ctx.Err() != nil {
		lastErr = ctx.Err()
	}
	if lastErr != nil {
		return nil, fmt.Errorf("1337x: no magnet for %s: %w", mediaID, lastErr)
	}
	return nil, fmt.Errorf("1337x: no release of %s has a magnet link", mediaID)
}

func (x *X1337) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

func (x *X1337) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("1337x: use Watch instead")
}

func (x *X1337) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}

func (x *X1337) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return nil, nil
}
