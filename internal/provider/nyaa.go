package provider

import (
	"encoding/xml"
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
	"unicode"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// nyaaBase is the Nyaa site root. A var so tests can point it at a stub.
var nyaaBase = "https://nyaa.si"

// nyaaCategory is "Anime - English-translated".
const nyaaCategory = "1_2"

// nyaaCacheTTL bounds how long a parsed feed is reused across the
// Search/GetEpisodes/GetServers/Watch steps of one play.
var nyaaCacheTTL = 5 * time.Minute

const nyaaCacheMax = 32

// nyaaTrackers are the announce URLs Nyaa puts in its own magnet links.
var nyaaTrackers = []string{
	"http://nyaa.tracker.wf:7777/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://tracker.torrent.eu.org:6969/announce",
}

const nyaaUserAgent = "Mozilla/5.0 (compatible; lobster/1.0; +https://github.com/billmal071/lobster)"

// Nyaa resolves anime episodes to BitTorrent magnets from the Nyaa RSS feed.
// Nyaa is a flat list of torrents; the provider parses release names into
// {group, series, season, episode, resolution}, groups them into series for
// Search and exposes per-episode torrents as servers. Watch returns a magnet.
type Nyaa struct {
	client *http.Client

	mu    sync.Mutex
	cache map[string]nyaaCacheEntry
}

type nyaaCacheEntry struct {
	items []nyaaRelease
	at    time.Time
}

func NewNyaa() *Nyaa {
	return &Nyaa{client: httputil.NewClient(), cache: map[string]nyaaCacheEntry{}}
}

// nyaaRelease is one torrent after its title has been parsed.
type nyaaRelease struct {
	Title    string // raw torrent title, used as the magnet display name
	Group    string // release group, "" when the title carries none
	Series   string // display series title with season markers removed
	Season   int    // 0 when the title has no season marker
	Episode  int    // 0 when the title has no episode number
	Height   int    // vertical resolution, 0 when unknown
	Year     string // "" unless the title carries a (YYYY)
	Batch    bool   // an episode range or a batch pack
	Hash     string
	Size     string
	Seeders  int
	Leechers int
}

type nyaaFeed struct {
	Items []nyaaItem `xml:"channel>item"`
}

type nyaaItem struct {
	Title    string `xml:"title"`
	Link     string `xml:"link"`
	Seeders  int    `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	Leechers int    `xml:"https://nyaa.si/xmlns/nyaa leechers"`
	Size     string `xml:"https://nyaa.si/xmlns/nyaa size"`
	InfoHash string `xml:"https://nyaa.si/xmlns/nyaa infoHash"`
	Category string `xml:"https://nyaa.si/xmlns/nyaa category"`
}

// nyaaFeedURL builds the RSS URL; sortBy "seeders" for Trending, "" for newest.
func nyaaFeedURL(query, sortBy string) string {
	v := url.Values{}
	v.Set("page", "rss")
	v.Set("f", "0")
	v.Set("c", nyaaCategory)
	if query != "" {
		v.Set("q", query)
	}
	if sortBy != "" {
		v.Set("s", sortBy)
		v.Set("o", "desc")
	}
	return strings.TrimRight(nyaaBase, "/") + "/?" + v.Encode()
}

// fetch returns the parsed releases for a feed URL, from cache when fresh.
// Unparsed items are kept with Episode 0 so callers can count them.
func (n *Nyaa) fetch(feedURL string) ([]nyaaRelease, error) {
	n.mu.Lock()
	if e, ok := n.cache[feedURL]; ok && time.Since(e.at) < nyaaCacheTTL {
		n.mu.Unlock()
		return e.items, nil
	}
	n.mu.Unlock()

	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", nyaaUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.8")
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nyaa request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nyaa: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, nyaaMaxFeedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading nyaa feed: %w", err)
	}
	if len(body) > nyaaMaxFeedBytes {
		return nil, fmt.Errorf("nyaa: feed exceeds the %d MiB size limit", nyaaMaxFeedBytes>>20)
	}
	var feed nyaaFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parsing nyaa feed: %w", err)
	}
	items := make([]nyaaRelease, 0, len(feed.Items))
	for _, it := range feed.Items {
		r := parseNyaaTitle(it.Title)
		r.Hash = strings.ToLower(strings.TrimSpace(it.InfoHash))
		r.Size = strings.TrimSpace(it.Size)
		r.Seeders = it.Seeders
		r.Leechers = it.Leechers
		items = append(items, r)
	}

	n.mu.Lock()
	n.evictLocked()
	n.cache[feedURL] = nyaaCacheEntry{items: items, at: time.Now()}
	n.mu.Unlock()
	return items, nil
}

// evictLocked drops expired feed cache entries, then the oldest ones until there is room for one more. The caller holds n.mu.
func (n *Nyaa) evictLocked() {
	for k, e := range n.cache {
		if time.Since(e.at) >= nyaaCacheTTL {
			delete(n.cache, k)
		}
	}
	for len(n.cache) >= nyaaCacheMax {
		oldestKey, oldestAt := "", time.Time{}
		for k, e := range n.cache {
			if oldestKey == "" || e.at.Before(oldestAt) {
				oldestKey, oldestAt = k, e.at
			}
		}
		delete(n.cache, oldestKey)
	}
}

// Title shapes seen on the live English-translated feed:
//
//	[SubsPlease] One-Punch Man S3 - 12 (1080p) [B7B8D40B].mkv
//	[Erai-raws] One Punch Man (2025) - 12 [1080p CR WEBRip HEVC AAC][MultiSub][FB20C752]
//	[Anime Time] One Punch Man - S03E12 [1080p][HEVC 10bit x265][AAC][Dual-Audio]
//	One Punch Man S03E12 Ultimate Lifeform 1080p CR WEB-DL DUAL AAC2.0 H 264-VARYG
//	One.Punch.Man.S03E11.1080p.CR.WEBRip.10bits.x265-Rapta
//	One-Punch Man Season 3 - 11 | S03 [English Dub][1080p]
//	[Erai-raws] Kusuriya no Hitorigoto 3rd Season - 06 [1080p CR WEBRip HEVC AAC]
//	[LeoSubs] The Great Ruler - Episode 98 - 1080p HEVC 10Bit - 60FPS - ENG
//	[ToonsHub] One Piece EP1158 1080p CR WEB-DL DUAL AAC2.0 H.264
//	One Piece S31E1156 The Long-sought Elbaph ... -VARYG            (absolute: episode 1156, no season)
//	One Piece S00E01 One Piece Heroines ... -VARYG                  (skipped: special)
//	[DubZoku] Jujutsu Kaisen S03E09-E12 v2 (WEB 1080p HEVC DDP)      (skipped: batch)
//	[SubsPlease] One-Punch Man S3 (00-12) (1080p) [Batch]            (skipped: batch)
//	[Ironclad] One-Punch Man - S03 [WEB.1080p.AV1] | One-Punch Man   (skipped: no episode)
var (
	nyaaExtRe   = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|webm|ts)$`)
	nyaaGroupRe = regexp.MustCompile(`^\[([^\]]+)\]\s*`)
	// A trailing "-GROUP" on scene-style names: "H 264-VARYG", "x265-Rapta".
	nyaaSceneGroupRe = regexp.MustCompile(`-([A-Za-z0-9]+)\s*$`)

	nyaaBatchWordRe  = regexp.MustCompile(`(?i)\bbatch\b`)
	nyaaBatchEpsRe   = regexp.MustCompile(`(?i)\bepisodes?\s+\d{1,4}\s*[-~]\s*\d{1,4}\b`)
	nyaaBatchParenRe = regexp.MustCompile(`\(\s*(\d{1,4})\s*[-~]\s*(\d{1,4})\s*\)`)
	// Both ends zero-padded, so "Title 2 - 05" is an episode and "01-12" a
	// range. The trailing \b refuses "12 - 1080p". A range ascends, which
	// is how "86 - 12" (the series 86, episode 12) is told apart.
	nyaaBatchRangeRe = regexp.MustCompile(`\b(\d{2,4})\s*[-~]\s*(\d{2,4})\b`)
	// "S03E09-E12", "S03E09-12", "E09-E12".
	nyaaBatchERangeRe = regexp.MustCompile(`(?i)\b(?:S\d{1,2})?E\d{1,4}\s*[-~]\s*E?\d{1,4}\b`)

	nyaaSxxExxRe   = regexp.MustCompile(`(?i)\bS(\d{1,2})E(\d{1,4})(?:v\d+)?\b`)
	nyaaEPRe       = regexp.MustCompile(`(?i)\bEP\s?(\d{1,4})(?:v\d+)?\b`)
	nyaaDashEpRe   = regexp.MustCompile(`\s[-–]\s(\d{1,4})(?:v\d+)?(?:\s|$|[\[(])`)
	nyaaEpisodeRe  = regexp.MustCompile(`(?i)\bEpisode\s+(\d{1,4})\b`)
	nyaaBareEpRe   = regexp.MustCompile(`^(.*?\D)\s+(\d{2,4})(?:v\d+)?\s*(?:$|-\s+\D)`)
	nyaaHeightRe   = regexp.MustCompile(`(?i)(\d{3,4})p\b`)
	nyaa4KRe       = regexp.MustCompile(`(?i)\b4k\b`)
	nyaaBracketRe  = regexp.MustCompile(`\[[^\]]*\]`)
	nyaaParenRe    = regexp.MustCompile(`\([^)]*\)`)
	nyaaYearRe     = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	nyaaBareYearRe = regexp.MustCompile(`\s((?:19|20)\d{2})$`)

	nyaaSeasonSRe       = regexp.MustCompile(`(?i)\bS(\d{1,2})\b`)
	nyaaSeasonWordRe    = regexp.MustCompile(`(?i)\bSeason\s*(\d{1,2})\b`)
	nyaaSeasonOrdinalRe = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)\s+Season\b`)
	nyaaSpaceRe         = regexp.MustCompile(`\s+`)
)

const nyaaMaxFeedBytes = 10 << 20

// nyaaYearSpan reports whether both ends are four-digit years in 1900-2099, which marks a "2019-2020" span rather than an episode range.
func nyaaYearSpan(lo, hi string) bool {
	isYear := func(v string) bool {
		return len(v) == 4 && (strings.HasPrefix(v, "19") || strings.HasPrefix(v, "20"))
	}
	return isYear(lo) && isYear(hi)
}

// nyaaParenBatch reports whether s has a parenthesised range such as "(01-12)" that is not a year span.
func nyaaParenBatch(s string) bool {
	for _, m := range nyaaBatchParenRe.FindAllStringSubmatch(s, -1) {
		if !nyaaYearSpan(m[1], m[2]) {
			return true
		}
	}
	return false
}

// nyaaAscendingRange reports whether s has a bare ascending number range such as "01-12", ignoring year spans.
func nyaaAscendingRange(s string) bool {
	for _, m := range nyaaBatchRangeRe.FindAllStringSubmatch(s, -1) {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		if nyaaYearSpan(m[1], m[2]) {
			continue
		}
		if lo < hi {
			return true
		}
	}
	return false
}

// nyaaCapturedTrailingYear reports whether captured, a number found in bare, is the four-digit year ending the name rather than an episode.
func nyaaCapturedTrailingYear(bare, captured string) bool {
	y := nyaaBareYearRe.FindStringSubmatch(bare)
	return y != nil && y[1] == captured
}

// parseNyaaTitle reads a torrent name. Episode is 0 when none was found and
// Batch is set for packs; both make the grouping skip the item.
func parseNyaaTitle(raw string) nyaaRelease {
	r := nyaaRelease{Title: strings.TrimSpace(raw)}
	s := nyaaExtRe.ReplaceAllString(r.Title, "")
	if m := nyaaGroupRe.FindStringSubmatch(s); m != nil {
		r.Group = strings.TrimSpace(m[1])
		s = s[len(m[0]):]
	}
	// Scene-style names have no spaces at all; dots are the separator.
	if !strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	if r.Group == "" {
		if m := nyaaSceneGroupRe.FindStringSubmatch(nyaaParenRe.ReplaceAllString(s, "")); m != nil {
			r.Group = m[1]
		}
	}
	if m := nyaaHeightRe.FindStringSubmatch(s); m != nil {
		r.Height, _ = strconv.Atoi(m[1])
	} else if nyaa4KRe.MatchString(s) {
		r.Height = 2160
	}
	if nyaaBatchWordRe.MatchString(s) || nyaaBatchEpsRe.MatchString(s) ||
		nyaaParenBatch(s) || nyaaAscendingRange(s) ||
		nyaaBatchERangeRe.MatchString(s) {
		r.Batch = true
		return r
	}

	var titlePart string
	absolute := false
	switch {
	case nyaaSxxExxRe.MatchString(s):
		loc := nyaaSxxExxRe.FindStringSubmatchIndex(s)
		r.Season, _ = strconv.Atoi(s[loc[2]:loc[3]])
		r.Episode, _ = strconv.Atoi(s[loc[4]:loc[5]])
		titlePart = s[:loc[0]]
		if r.Season == 0 {
			// S00Exx is a special, not episode xx of the series.
			r.Episode = 0
			return r
		}
		// "S31E1156", "S01E1178": absolute numbering wearing a season tag.
		absolute = loc[5]-loc[4] >= 4 || (r.Season >= 20 && r.Episode > 100)
	case nyaaEPRe.MatchString(s):
		loc := nyaaEPRe.FindStringSubmatchIndex(s)
		r.Episode, _ = strconv.Atoi(s[loc[2]:loc[3]])
		titlePart = s[:loc[0]]
	case nyaaDashEpRe.MatchString(s):
		loc := nyaaDashEpRe.FindStringSubmatchIndex(s)
		r.Episode, _ = strconv.Atoi(s[loc[2]:loc[3]])
		titlePart = s[:loc[0]]
	case nyaaEpisodeRe.MatchString(s):
		loc := nyaaEpisodeRe.FindStringSubmatchIndex(s)
		r.Episode, _ = strconv.Atoi(s[loc[2]:loc[3]])
		titlePart = s[:loc[0]]
	default:
		// "Welsh And Shedar 01", "Beyblade X 138 - The Kingdom's Way".
		// A trailing year ("Some Film 2016") is a release date, not
		// episode 2016 of a series.
		bare := strings.TrimSpace(nyaaParenRe.ReplaceAllString(nyaaBracketRe.ReplaceAllString(s, ""), ""))
		if m := nyaaBareEpRe.FindStringSubmatch(bare); m != nil && !nyaaCapturedTrailingYear(bare, m[2]) {
			r.Episode, _ = strconv.Atoi(m[2])
			titlePart = m[1]
		}
	}
	if r.Episode == 0 {
		return r
	}
	r.Series, r.Year = nyaaCleanSeries(titlePart, &r.Season)
	if r.Series == "" {
		r.Episode = 0
	}
	if absolute {
		r.Season = 0
	}
	return r
}

// nyaaCleanSeries strips tags, years, alternate titles and season markers
// from the text before the episode marker. A season found is written through
// season when it is still unset.
func nyaaCleanSeries(part string, season *int) (series, year string) {
	s := nyaaBracketRe.ReplaceAllString(part, " ")
	// "Title | Alternate Title": keep the first.
	if i := strings.Index(s, " | "); i >= 0 {
		s = s[:i]
	}
	if m := nyaaYearRe.FindStringSubmatch(s); m != nil {
		year = m[1]
		s = strings.Replace(s, m[0], " ", 1)
	}
	s = nyaaParenRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if !strings.Contains(s, " ") {
		s = strings.ReplaceAll(s, ".", " ")
	}
	s = strings.TrimSpace(s)
	if year == "" {
		if m := nyaaBareYearRe.FindStringSubmatch(s); m != nil {
			year = m[1]
			s = strings.TrimSuffix(s, m[0])
		}
	}
	for _, re := range []*regexp.Regexp{nyaaSeasonOrdinalRe, nyaaSeasonWordRe, nyaaSeasonSRe} {
		m := re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		if *season == 0 {
			*season, _ = strconv.Atoi(m[1])
		}
		s = strings.Replace(s, m[0], " ", 1)
	}
	s = nyaaSpaceRe.ReplaceAllString(s, " ")
	s = strings.Trim(s, " -–:.")
	return strings.TrimSpace(s), year
}

// nyaaKey normalises a series title for grouping: lower-case letters and
// digits, punctuation as a space except apostrophes, which are elided so
// "Journey's End" and "Journeys End" agree. Season 1 and no season share a
// key; sequels get their own.
func nyaaKey(series string, season int) string {
	var b strings.Builder
	space := false
	for _, c := range strings.ToLower(series) {
		switch {
		case c == '\'' || c == '’' || c == '`':
		case unicode.IsLetter(c) || unicode.IsDigit(c):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(c)
		default:
			space = true
		}
	}
	if season >= 2 {
		return b.String() + "|s" + strconv.Itoa(season)
	}
	return b.String()
}

// nyaaSeries is the grouping of releases under one series key.
type nyaaSeries struct {
	Key      string
	Title    string // display title, "Series Season N" for sequels
	Base     string // series title without the season, the search query
	Season   int
	Year     string
	Releases []nyaaRelease
}

// nyaaGroupKey is nyaaKey with the adaptation's year, when a release carries
// one: same-title adaptations from different years are different series.
func nyaaGroupKey(series string, season int, year string) string {
	key := nyaaKey(series, season)
	if year != "" {
		key += "|y" + year
	}
	return key
}

// groupNyaa buckets playable releases by series in order of first appearance.
// Batches and unparsed titles are dropped; their count is returned.
//
// Year is part of a series' identity. Releases that carry none join the one
// year-tagged series of that title when there is exactly one — most groups
// tag the year, some do not, and it is the same show — but when the feed
// holds two adaptations they stay a series of their own rather than being
// dealt to either one.
func groupNyaa(items []nyaaRelease) (series []*nyaaSeries, skipped int) {
	byKey := map[string]*nyaaSeries{}
	for _, r := range items {
		if r.Batch || r.Episode == 0 || r.Hash == "" {
			skipped++
			continue
		}
		key := nyaaGroupKey(r.Series, r.Season, r.Year)
		s, ok := byKey[key]
		if !ok {
			s = &nyaaSeries{Key: key, Base: r.Series, Title: r.Series, Season: r.Season, Year: r.Year}
			if r.Season >= 2 {
				s.Title = fmt.Sprintf("%s Season %d", r.Series, r.Season)
			}
			byKey[key] = s
			series = append(series, s)
		}
		s.Releases = append(s.Releases, r)
	}
	// Fold each undated series into its title's sole dated one.
	dated := map[string][]*nyaaSeries{}
	for _, s := range series {
		if s.Year != "" {
			k := nyaaKey(s.Base, s.Season)
			dated[k] = append(dated[k], s)
		}
	}
	kept := series[:0]
	for _, s := range series {
		if s.Year == "" {
			if ds := dated[nyaaKey(s.Base, s.Season)]; len(ds) == 1 {
				ds[0].Releases = append(ds[0].Releases, s.Releases...)
				continue
			}
		}
		kept = append(kept, s)
	}
	return kept, skipped
}

// nyaaID encodes a series as "nyaa/<escaped base title>", plus "s=N" for a
// sequel and "y=YYYY" for a dated adaptation, so the search query stays the
// bare title.
func nyaaID(base string, season int, year string) string {
	id := "nyaa/" + url.QueryEscape(base)
	q := url.Values{}
	if season >= 2 {
		q.Set("s", strconv.Itoa(season))
	}
	if year != "" {
		q.Set("y", year)
	}
	if len(q) > 0 {
		id += "?" + q.Encode()
	}
	return id
}

// parseNyaaID splits a "nyaa/<base>?s=<season>&y=<year>" series ID into its base title, season and year.
func parseNyaaID(id string) (base string, season int, year string, err error) {
	rest, ok := strings.CutPrefix(id, "nyaa/")
	if !ok || rest == "" {
		return "", 0, "", fmt.Errorf("nyaa: invalid series ID %q", id)
	}
	if b, q, found := strings.Cut(rest, "?"); found {
		rest = b
		v, _ := url.ParseQuery(q)
		season, _ = strconv.Atoi(v.Get("s"))
		year = v.Get("y")
	}
	base, err = url.QueryUnescape(rest)
	if err != nil || strings.TrimSpace(base) == "" {
		return "", 0, "", fmt.Errorf("nyaa: invalid series ID %q", id)
	}
	return base, season, year, nil
}

// nyaaResults maps grouped series to search results, counting each series' distinct episodes.
func nyaaResults(series []*nyaaSeries) []media.SearchResult {
	out := make([]media.SearchResult, 0, len(series))
	for _, s := range series {
		eps := map[int]bool{}
		for _, r := range s.Releases {
			eps[r.Episode] = true
		}
		out = append(out, media.SearchResult{
			ID:       nyaaID(s.Base, s.Season, s.Year),
			Title:    s.Title,
			Year:     s.Year,
			Type:     media.TV,
			Seasons:  1,
			Episodes: len(eps),
			Duration: fmt.Sprintf("EPS %d", len(eps)),
		})
	}
	return out
}

// Search returns one result per series in the feed (capped at 75 torrents by
// the site), series with the most torrents first.
func (n *Nyaa) Search(query string) ([]media.SearchResult, error) {
	items, err := n.fetch(nyaaFeedURL(strings.TrimSpace(query), ""))
	if err != nil {
		return nil, err
	}
	series, _ := groupNyaa(items)
	sort.SliceStable(series, func(i, j int) bool {
		return len(series[i].Releases) > len(series[j].Releases)
	})
	return nyaaResults(series), nil
}

// seriesReleases re-runs the search for a series ID and keeps its releases.
//
// A dated ID names one adaptation exactly. An undated one (an older ref, or a
// series whose releases carry no year) takes the undated series when there is
// one, else the title's only dated series; several dated ones are ambiguous.
func (n *Nyaa) seriesReleases(id string) (*nyaaSeries, error) {
	base, season, year, err := parseNyaaID(id)
	if err != nil {
		return nil, err
	}
	items, err := n.fetch(nyaaFeedURL(base, ""))
	if err != nil {
		return nil, err
	}
	series, _ := groupNyaa(items)
	want := nyaaGroupKey(base, season, year)
	var dated []*nyaaSeries
	for _, s := range series {
		if s.Key == want {
			return s, nil
		}
		if year == "" && nyaaKey(s.Base, s.Season) == want {
			dated = append(dated, s)
		}
	}
	switch len(dated) {
	case 0:
	case 1:
		return dated[0], nil
	default:
		return nil, fmt.Errorf("nyaa: %s is ambiguous: %d adaptations by year match, pick one from search", id, len(dated))
	}
	return nil, fmt.Errorf("nyaa: no episodes found for %s", id)
}

// GetSeasons reports the one season the series ID names (1 when unmarked); a
// sequel is its own series in Search.
func (n *Nyaa) GetSeasons(id string) ([]media.Season, error) {
	_, season, _, err := parseNyaaID(id)
	if err != nil {
		return nil, err
	}
	if season == 0 {
		season = 1
	}
	return []media.Season{{Number: season, ID: strconv.Itoa(season)}}, nil
}

// GetEpisodes lists the distinct episode numbers the feed currently carries.
func (n *Nyaa) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	s, err := n.seriesReleases(id)
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var nums []int
	for _, r := range s.Releases {
		if !seen[r.Episode] {
			seen[r.Episode] = true
			nums = append(nums, r.Episode)
		}
	}
	sort.Ints(nums)
	out := make([]media.Episode, 0, len(nums))
	for _, e := range nums {
		out = append(out, media.Episode{Number: e, ID: strconv.Itoa(e), Title: fmt.Sprintf("Episode %d", e)})
	}
	return out, nil
}

// nyaaEpisodeRef parses an episode ID. Native IDs are "<n>"; the fallback
// resolver sends "<showID>:<season>:<episode>" (season returned non-zero).
// An empty ID is a movie request, which Nyaa's series cannot serve.
func nyaaEpisodeRef(episodeID string) (season, episode int, err error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return 0, 0, fmt.Errorf("nyaa: TV only, no episode requested")
	}
	bad := fmt.Errorf("nyaa: invalid episode ID %q", episodeID)
	if i := strings.LastIndex(episodeID, ":"); i >= 0 {
		j := strings.LastIndex(episodeID[:i], ":")
		if j < 0 {
			return 0, 0, bad
		}
		season, err = strconv.Atoi(episodeID[j+1 : i])
		if err != nil || season <= 0 {
			return 0, 0, bad
		}
		episodeID = episodeID[i+1:]
	}
	episode, err = strconv.Atoi(episodeID)
	if err != nil || episode <= 0 {
		return 0, 0, bad
	}
	return season, episode, nil
}

// episodeReleases returns the torrents for one episode, highest resolution
// first and best-seeded within a resolution. Unseeded torrents are dropped
// when a seeded one exists. A fallback-form episode ID naming season N >= 2
// against an unmarked series ID is redirected to that series' "?s=N" ID; one
// naming a season other than a marked ID's is refused, so the resolver can
// try another candidate rather than play the wrong season. Season 1 is the
// synthetic season every Nyaa series reports (GetSeasons) and always fits.
func (n *Nyaa) episodeReleases(id, episodeID string) ([]nyaaRelease, error) {
	season, ep, err := nyaaEpisodeRef(episodeID)
	if err != nil {
		return nil, err
	}
	if season >= 2 {
		base, idSeason, year, err := parseNyaaID(id)
		if err != nil {
			return nil, err
		}
		switch {
		case idSeason == 0:
			id = nyaaID(base, season, year)
		case idSeason != season:
			return nil, fmt.Errorf("nyaa: %s is season %d, not season %d", id, idSeason, season)
		}
	}
	s, err := n.seriesReleases(id)
	if err != nil {
		return nil, err
	}
	var out, seeded []nyaaRelease
	for _, r := range s.Releases {
		if r.Episode != ep {
			continue
		}
		out = append(out, r)
		if r.Seeders > 0 {
			seeded = append(seeded, r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nyaa: no torrents for %s episode %d", id, ep)
	}
	if len(seeded) > 0 {
		out = seeded
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height > out[j].Height
		}
		return out[i].Seeders > out[j].Seeders
	})
	return out, nil
}

// nyaaQuality renders a height the way the cmd-side quality preference
// expects it at the start of a server name.
func nyaaQuality(height int) string {
	if height <= 0 {
		return "unknown"
	}
	return strconv.Itoa(height) + "p"
}

// nyaaServerName is the name a user picks a release by: quality, release group ("untagged" when none), size and seeders.
func nyaaServerName(r nyaaRelease) string {
	group := r.Group
	if group == "" {
		group = "untagged"
	}
	return fmt.Sprintf("%s %s (%s, %d seeds)", nyaaQuality(r.Height), group, r.Size, r.Seeders)
}

// GetServers lists one entry per torrent for the episode, seed count included.
func (n *Nyaa) GetServers(id string, episodeID string) ([]media.Server, error) {
	rels, err := n.episodeReleases(id, episodeID)
	if err != nil {
		return nil, err
	}
	servers := make([]media.Server, 0, len(rels))
	for i, r := range rels {
		servers = append(servers, media.Server{Name: nyaaServerName(r), ID: strconv.Itoa(i)})
	}
	return servers, nil
}

// nyaaPickTorrent chooses by explicit server name (or index) first, then the
// requested quality, then the highest known quality below it, then the first
// entry, which is the highest available.
func nyaaPickTorrent(rels []nyaaRelease, server, quality string) nyaaRelease {
	for i, r := range rels {
		if server != "" && (strings.EqualFold(server, nyaaServerName(r)) || server == strconv.Itoa(i)) {
			return r
		}
	}
	if want := strings.ToLower(strings.TrimSpace(quality)); want != "" && want != "best" {
		if h, err := strconv.Atoi(strings.TrimSuffix(want, "p")); err == nil {
			lower, lowerH := -1, 0
			for i, r := range rels {
				if r.Height == h {
					return r
				}
				if r.Height < h && r.Height > lowerH {
					lower, lowerH = i, r.Height
				}
			}
			if lower >= 0 {
				return rels[lower]
			}
		}
	}
	return rels[0]
}

// nyaaMagnet builds a magnet URI with Nyaa's own trackers attached.
func nyaaMagnet(hash, title string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(hash)
	b.WriteString("&dn=")
	b.WriteString(url.QueryEscape(title))
	for _, tr := range nyaaTrackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

// Watch returns a magnet URI for the torrent server to turn into a stream.
func (n *Nyaa) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	rels, err := n.episodeReleases(mediaID, episodeID)
	if err != nil {
		return nil, err
	}
	r := nyaaPickTorrent(rels, server, quality)
	return &media.Stream{
		URL:     nyaaMagnet(r.Hash, r.Title),
		Quality: nyaaQuality(r.Height),
	}, nil
}

// GetDetails describes a series by its year, torrent count and release groups.
func (n *Nyaa) GetDetails(id string) (*media.ContentDetail, error) {
	s, err := n.seriesReleases(id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var groups []string
	for _, r := range s.Releases {
		if r.Group != "" && !seen[r.Group] {
			seen[r.Group] = true
			groups = append(groups, r.Group)
		}
	}
	return &media.ContentDetail{
		Released:    s.Year,
		Description: fmt.Sprintf("%d torrents on Nyaa from %s", len(s.Releases), strings.Join(groups, ", ")),
	}, nil
}

func (n *Nyaa) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("nyaa: use Watch instead")
}

// Trending lists the series behind the best-seeded torrents in the category.
func (n *Nyaa) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	if mediaType == media.Movie {
		return nil, fmt.Errorf("%w: nyaa lists series only", ErrNoResults)
	}
	return n.browse("seeders")
}

// Recent lists the series behind the newest uploads in the category.
func (n *Nyaa) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	if mediaType == media.Movie {
		return nil, fmt.Errorf("%w: nyaa lists series only", ErrNoResults)
	}
	return n.browse("")
}

// browse lists the series behind the category feed ordered by sortBy ("" is newest first).
func (n *Nyaa) browse(sortBy string) ([]media.SearchResult, error) {
	items, err := n.fetch(nyaaFeedURL("", sortBy))
	if err != nil {
		return nil, err
	}
	series, _ := groupNyaa(items)
	return nyaaResults(series), nil
}
