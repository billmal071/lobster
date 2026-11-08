package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"lobster/internal/httputil"
	"lobster/internal/lang"
	"lobster/internal/media"
)

const (
	tbcplDefaultBase = "https://www.1shows.org"
	tbcplTMDBBase    = "https://www.themoviedb.org"
	// tbcplVidzeeBase is the embed player origin, sent as the Referer.
	tbcplVidzeeBase = "https://player.vidzee.wtf"
	// tbcplVidzeeAPIBase serves /streams/<path>?s=<server>,
	// /streams/languages/<path> and /subs/<path>.
	tbcplVidzeeAPIBase = "https://core.vidzee.wtf"
	tbcplMaxSeasons    = 30
)

// tbcplWatchBudget bounds one Watch call end to end: the languages lookup,
// every server attempt and the subtitle fetch. It stays under the resolver's
// 30s per-attempt timeout. A variable so tests can shrink it.
var tbcplWatchBudget = 20 * time.Second

// TBCPL bridges the TBCPL index to the currently working 1Shows catalog and
// Vidzee stream API. TBCPL itself is an index of sources, not a direct media API.
type TBCPL struct {
	base          string
	client        *http.Client
	tmdbBaseURL   string
	vidzeeBaseURL string
	vidzeeAPIURL  string

	mu          sync.RWMutex
	searchCache map[string]tbcplCatalogItem
	seasonCache map[string]*tbcplSeasonResponse
	audioLang   string
}

// SetAudioLanguage sets the preferred audio-track language used to order
// Vidzee's per-dub sources. Empty keeps the provider's own order.
func (t *TBCPL) SetAudioLanguage(lang string) {
	t.mu.Lock()
	t.audioLang = lang
	t.mu.Unlock()
}

// NewTBCPL creates a new TBCPL provider.
func NewTBCPL(base string) *TBCPL {
	// Use a custom transport that resolves player.vidzee.wtf to known Cloudflare IPs.
	// Some networks/DNS resolvers fail to resolve this subdomain.
	baseTransport := httputil.NewClient().Transport.(*http.Transport).Clone()
	baseTransport.DialContext = vidzeeDialer()

	return &TBCPL{
		base:          normalizeTBCPLBase(base),
		client:        &http.Client{Timeout: 8 * time.Second, Transport: baseTransport},
		tmdbBaseURL:   tbcplTMDBBase,
		vidzeeBaseURL: tbcplVidzeeBase,
		vidzeeAPIURL:  tbcplVidzeeAPIBase,
		audioLang:     "english",
		searchCache:   make(map[string]tbcplCatalogItem),
		seasonCache:   make(map[string]*tbcplSeasonResponse),
	}
}

// vidzeeDialer returns a DialContext that resolves vidzee.wtf subdomains
// to known Cloudflare IPs when system DNS fails.
func vidzeeDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	fallbackIPs := []string{"172.67.180.227", "104.21.75.193"}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(addr)
		// Try system DNS first.
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err == nil {
			return conn, nil
		}
		// Fallback to hardcoded IPs for vidzee.wtf subdomains.
		if !strings.Contains(host, "vidzee.wtf") {
			return nil, err
		}
		for _, ip := range fallbackIPs {
			conn, err = dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("all vidzee IPs failed for %s", addr)
	}
}

func normalizeTBCPLBase(base string) string {
	base = strings.TrimSpace(strings.TrimRight(base, "/"))
	lower := strings.ToLower(base)
	if base == "" || lower == "tbcpl" || strings.Contains(lower, "tbcpl") {
		return tbcplDefaultBase
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	return strings.TrimRight(base, "/")
}

func (t *TBCPL) baseURL() string {
	return strings.TrimRight(t.base, "/")
}

type tbcplSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tbcplCatalogItem struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	MediaType    string  `json:"media_type"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
}

func (i tbcplCatalogItem) displayTitle() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Title
}

func (i tbcplCatalogItem) year() string {
	date := i.ReleaseDate
	if date == "" {
		date = i.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func (i tbcplCatalogItem) released() string {
	if i.ReleaseDate != "" {
		return i.ReleaseDate
	}
	return i.FirstAirDate
}

type tbcplSeasonResponse struct {
	Name         string         `json:"name"`
	SeasonNumber int            `json:"season_number"`
	Episodes     []tbcplEpisode `json:"episodes"`
}

type tbcplEpisode struct {
	EpisodeNumber int    `json:"episode_number"`
	SeasonNumber  int    `json:"season_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	Runtime       int    `json:"runtime"`
}

type tbcplDirectServer struct {
	Name string
	Slug string // the s= query value
}

// tbcplDirectServers are Vidzee's language-neutral ("Auto" audio) servers.
// Vidzee's own default, "peak", is absent: its CDN needs an Origin header
// nothing in lobster sends.
var tbcplDirectServers = []tbcplDirectServer{
	{Name: "DCloud", Slug: "dcloud"},
	{Name: "Tik", Slug: "tik"},
}

// tbcplDubServerPrefix marks a per-language Vidzee server, e.g. "v6:Hindi".
const tbcplDubServerPrefix = "v6:"

// Search uses TMDB's keyless multi-search endpoint for reliable title lookup.
// The returned TMDB IDs are then used by 1Shows/Vidzee.
func (t *TBCPL) Search(query string) ([]media.SearchResult, error) {
	searchURL := tmdbMultiSearchURL(t.tmdbBaseURL, query)

	body, err := t.fetch(searchURL, strings.TrimRight(t.tmdbBaseURL, "/")+"/", "application/json, text/html, */*")
	if err != nil {
		return nil, fmt.Errorf("tbcpl search: %w", err)
	}

	results, err := t.parseCatalogResponse(body, "")
	if err != nil {
		return nil, fmt.Errorf("tbcpl search: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%w for %q", ErrNoResults, query)
	}
	return results, nil
}

// GetDetails returns cached metadata from search results. 1Shows detail
// endpoints currently reject direct server-side requests, so this is best-effort.
func (t *TBCPL) GetDetails(id string) (*media.ContentDetail, error) {
	t.mu.RLock()
	item, ok := t.searchCache[id]
	t.mu.RUnlock()
	if !ok {
		return &media.ContentDetail{}, nil
	}

	return &media.ContentDetail{
		Description: item.Overview,
		Rating:      formatTBCPLRating(item.VoteAverage),
		Released:    item.released(),
	}, nil
}

// GetSeasons returns available TV seasons by probing 1Shows' season API.
func (t *TBCPL) GetSeasons(id string) ([]media.Season, error) {
	tmdbID := extractTMDBID(id)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, err
	}

	var seasons []media.Season
	for season := 1; season <= tbcplMaxSeasons; season++ {
		resp, err := t.fetchSeason(tmdbID, season)
		if err != nil {
			if season == 1 {
				return nil, fmt.Errorf("getting seasons: %w", err)
			}
			break
		}
		if len(resp.Episodes) == 0 {
			break
		}

		number := resp.SeasonNumber
		if number == 0 {
			number = season
		}
		seasons = append(seasons, media.Season{
			Number: number,
			ID:     fmt.Sprintf("%s:%d", tmdbID, number),
		})
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found")
	}
	return seasons, nil
}

// GetEpisodes returns episodes for a TV season from 1Shows' season API.
func (t *TBCPL) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	tmdbID, season, err := parseTBCPLSeasonID(id, seasonID)
	if err != nil {
		return nil, err
	}

	resp, err := t.fetchSeason(tmdbID, season)
	if err != nil {
		return nil, fmt.Errorf("getting episodes: %w", err)
	}

	episodes := make([]media.Episode, 0, len(resp.Episodes))
	for _, ep := range resp.Episodes {
		seasonNum := ep.SeasonNumber
		if seasonNum == 0 {
			seasonNum = season
		}
		if ep.EpisodeNumber <= 0 {
			continue
		}
		episodes = append(episodes, media.Episode{
			Number: ep.EpisodeNumber,
			Title:  ep.Name,
			ID:     fmt.Sprintf("%s:%d:%d", tmdbID, seasonNum, ep.EpisodeNumber),
		})
	}
	if len(episodes) == 0 {
		return nil, fmt.Errorf("no episodes found")
	}
	return episodes, nil
}

// GetServers returns direct Vidzee server choices: the language-neutral
// servers plus one "v6:<Language>" entry per dub Vidzee advertises for the
// title. Watch performs the actual stream resolution.
func (t *TBCPL) GetServers(id string, episodeID string) ([]media.Server, error) {
	path, err := tbcplStreamPath(id, episodeID)
	if err != nil {
		return nil, err
	}
	srcs := t.serverCandidates(context.Background(), path)
	servers := make([]media.Server, 0, len(srcs))
	for _, src := range srcs {
		servers = append(servers, media.Server{
			Name: src.Name,
			ID:   "vidzee:" + src.Link,
		})
	}
	return servers, nil
}

// GetEmbedURL supports callers that treat server IDs as public embed URLs.
// TBCPL's direct Vidzee servers must use Watch instead.
func (t *TBCPL) GetEmbedURL(serverID string) (string, error) {
	if strings.HasPrefix(serverID, "https://") {
		return serverID, nil
	}
	return "", fmt.Errorf("use Watch for TBCPL server %q", serverID)
}

// Trending returns 1Shows trending titles for the requested media type.
func (t *TBCPL) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	apiURL := fmt.Sprintf("%s/api/trending/%s/day", t.baseURL(), mt)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return nil, fmt.Errorf("tbcpl trending: %w", err)
	}
	return t.parseCatalogResponse(body, mt)
}

// Recent returns recent-ish content using the 1Shows weekly trending endpoint.
func (t *TBCPL) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	apiURL := fmt.Sprintf("%s/api/trending/%s/week", t.baseURL(), mt)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return t.Trending(mediaType)
	}
	return t.parseCatalogResponse(body, mt)
}

// Watch resolves a direct HLS stream through Vidzee's stream API. The whole
// call, languages lookup and subtitles included, finishes within
// tbcplWatchBudget.
func (t *TBCPL) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	path, err := tbcplStreamPath(mediaID, episodeID)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), tbcplWatchBudget)
	defer cancel()

	var candidates []tbcplVidzeeSource
	if srv, ok := t.matchDirectServer(server); ok {
		candidates = []tbcplVidzeeSource{srv}
	} else {
		candidates = t.serverCandidates(ctx, path)
	}

	var lastErr error
	for _, srv := range candidates {
		if ctx.Err() != nil {
			break
		}
		stream, err := t.watchWithServer(ctx, path, srv, quality)
		if err == nil {
			return stream, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all servers timed out")
	}
	return nil, fmt.Errorf("tbcpl watch failed: %w", lastErr)
}

func (t *TBCPL) parseCatalogResponse(body []byte, fallbackMediaType string) ([]media.SearchResult, error) {
	var resp tbcplSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	results := make([]media.SearchResult, 0, len(resp.Results))
	for _, raw := range resp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}

		var item tbcplCatalogItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.ID == 0 {
			continue
		}
		if item.MediaType == "" {
			item.MediaType = fallbackMediaType
		}
		if item.MediaType != "movie" && item.MediaType != "tv" {
			continue
		}
		title := item.displayTitle()
		if title == "" {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}
		id := fmt.Sprintf("%s/%d", item.MediaType, item.ID)
		t.cacheCatalogItem(id, item)

		results = append(results, media.SearchResult{
			ID:    id,
			Title: title,
			Type:  mt,
			Year:  item.year(),
			URL:   fmt.Sprintf("%s/%s/%d", t.baseURL(), item.MediaType, item.ID),
		})
	}

	return results, nil
}

func (t *TBCPL) cacheCatalogItem(id string, item tbcplCatalogItem) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.searchCache[id] = item
}

func (t *TBCPL) fetchSeason(tmdbID string, season int) (*tbcplSeasonResponse, error) {
	key := fmt.Sprintf("%s:%d", tmdbID, season)

	t.mu.RLock()
	cached := t.seasonCache[key]
	t.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	apiURL := fmt.Sprintf("%s/api/tv/%s/season/%d", t.baseURL(), tmdbID, season)
	body, err := t.fetch(apiURL, t.baseURL()+"/", "application/json, */*")
	if err != nil {
		return nil, err
	}

	var resp tbcplSeasonResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing season response: %w", err)
	}

	t.mu.Lock()
	t.seasonCache[key] = &resp
	t.mu.Unlock()

	return &resp, nil
}

// tbcplStreamPath builds the API path segment shared by /streams, /subs and
// /streams/languages: "movie/<tmdb>" or "tv/<tmdb>/<season>/<episode>".
func tbcplStreamPath(mediaID, episodeID string) (string, error) {
	if episodeID != "" {
		tmdbID, season, episode, err := parseTBCPLEpisodeID(mediaID, episodeID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("tv/%s/%d/%d", tmdbID, season, episode), nil
	}
	tmdbID := extractTMDBID(mediaID)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", err
	}
	return "movie/" + tmdbID, nil
}

func (t *TBCPL) vidzeeAPI(path string) string {
	return strings.TrimRight(t.vidzeeAPIURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func (t *TBCPL) vidzeeReferer() string {
	return strings.TrimRight(t.vidzeeBaseURL, "/") + "/"
}

// serverCandidates lists the servers Watch rotates through for one title, in
// the order to try them: the preferred audio language's dub server first
// (v6:English by default), then the language-neutral servers (Lang "Auto"),
// then one dub server per language Vidzee advertises, then v6:English.
//
// The preferred dub and v6:English are included whether or not
// /streams/languages lists them: that listing is incomplete (Fight Club
// advertises only Hindi while v6:English streams), and an unstocked dub fails
// fast upstream (404 "No stream"), so the extra candidates are cheap. The
// languages lookup itself is best-effort.
func (t *TBCPL) serverCandidates(ctx context.Context, path string) []tbcplVidzeeSource {
	t.mu.RLock()
	pref := t.audioLang
	t.mu.RUnlock()

	srcs := make([]tbcplVidzeeSource, 0, len(tbcplDirectServers)+5)
	seen := make(map[string]bool, cap(srcs))
	addDub := func(lang string) {
		if lang = tbcplDubLanguage(lang); lang == "" {
			return
		}
		slug := tbcplDubServerPrefix + lang
		if seen[slug] {
			return
		}
		seen[slug] = true
		srcs = append(srcs, tbcplVidzeeSource{Lang: lang, Link: slug, Name: slug})
	}

	// Through the alias table, so an ISO preference ("ta") names the dub
	// server Vidzee has ("v6:Tamil"), not one it does not ("v6:Ta").
	addDub(lang.Canonical(pref))
	for _, srv := range tbcplDirectServers {
		srcs = append(srcs, tbcplVidzeeSource{Lang: "Auto", Link: srv.Slug, Name: srv.Name})
	}
	for _, lang := range t.fetchVidzeeLanguages(ctx, path) {
		addDub(lang)
	}
	addDub("English")
	return preferAudioLang(srcs, pref)
}

// tbcplDubLanguage spells a language the way Vidzee's v6: servers expect:
// first letter upper, the rest lower ("v6:english" is 400 Unknown server).
func tbcplDubLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(lang)
	return string(unicode.ToUpper(r)) + lang[size:]
}

// fetchVidzeeLanguages returns the non-empty audio languages Vidzee lists for path, or nil when the request or parse fails.
func (t *TBCPL) fetchVidzeeLanguages(ctx context.Context, path string) []string {
	body, err := t.fetchCtx(ctx, t.vidzeeAPI("streams/languages/"+path), t.vidzeeReferer(), "application/json, */*")
	if err != nil {
		return nil
	}
	var resp struct {
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	out := resp.Languages[:0]
	for _, l := range resp.Languages {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// watchWithServer asks one Vidzee server for a stream and attaches its subtitles, referer and headers.
func (t *TBCPL) watchWithServer(ctx context.Context, path string, server tbcplVidzeeSource, quality string) (*media.Stream, error) {
	values := url.Values{}
	values.Set("s", server.Link)

	body, err := t.fetchCtx(ctx, t.vidzeeAPI("streams/"+path)+"?"+values.Encode(), t.vidzeeReferer(), "application/json, */*")
	if err != nil {
		return nil, fmt.Errorf("vidzee %s: %w", server.Name, err)
	}

	var resp tbcplVidzeeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing Vidzee response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("vidzee %s: %s", server.Name, resp.Error)
	}
	if strings.TrimSpace(resp.URL) == "" {
		return nil, fmt.Errorf("vidzee %s returned no stream URL", server.Name)
	}

	stream := &media.Stream{
		URL:       resp.URL,
		Quality:   quality,
		Subtitles: t.fetchVidzeeSubtitles(ctx, path),
		Referer:   t.vidzeeReferer(),
	}
	// The API's "headers" are what the CDN wants on playlist and segment
	// requests. Referer and User-Agent are the two the players and hlsproxy
	// can send; the CDN's own Referer wins over the player origin.
	for k, v := range resp.Headers {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		switch strings.ToLower(k) {
		case "referer":
			stream.Referer = v
		case "user-agent":
			stream.UserAgent = v
		}
	}
	return stream, nil
}

// tbcplVidzeeResponse is one plain-JSON /streams reply (no e=1).
type tbcplVidzeeResponse struct {
	Error    string            `json:"error"`
	Language string            `json:"language"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
}

// tbcplVidzeeSource is one playable server for a title. Link carries the s=
// query value and Lang the audio it serves ("Auto" for the neutral servers),
// which is what preferAudioLang orders by.
type tbcplVidzeeSource struct {
	Lang string
	Link string
	Name string
}

type tbcplVidzeeSubtitle struct {
	Label string `json:"label"`
	File  string `json:"file"`
}

// fetchVidzeeSubtitles is best-effort: a stream without subtitles is still a
// stream.
func (t *TBCPL) fetchVidzeeSubtitles(ctx context.Context, path string) []media.Subtitle {
	body, err := t.fetchCtx(ctx, t.vidzeeAPI("subs/"+path), t.vidzeeReferer(), "application/json, */*")
	if err != nil {
		return nil
	}
	var tracks []tbcplVidzeeSubtitle
	if err := json.Unmarshal(body, &tracks); err != nil {
		return nil
	}
	return mapTBCPLVidzeeTracks(tracks)
}

// mapTBCPLVidzeeTracks converts Vidzee subtitle tracks to media subtitles, skipping tracks with no file.
func mapTBCPLVidzeeTracks(tracks []tbcplVidzeeSubtitle) []media.Subtitle {
	subs := make([]media.Subtitle, 0, len(tracks))
	for _, tr := range tracks {
		if tr.File == "" {
			continue
		}
		label := tr.Label
		if label == "" {
			label = "Subtitle"
		}
		subs = append(subs, media.Subtitle{
			Language: tr.Label,
			Label:    label,
			URL:      tr.File,
		})
	}
	return subs
}

// matchDirectServer resolves a caller-supplied server name or ID ("DCloud",
// "vidzee:tik", "v6:Hindi") to a single candidate. Empty and "Default" mean
// the full rotation.
func (t *TBCPL) matchDirectServer(server string) (tbcplVidzeeSource, bool) {
	server = strings.TrimSpace(server)
	if server == "" || strings.EqualFold(server, "default") {
		return tbcplVidzeeSource{}, false
	}
	if strings.HasPrefix(strings.ToLower(server), "vidzee:") {
		server = server[len("vidzee:"):]
	}

	for _, srv := range tbcplDirectServers {
		if strings.EqualFold(server, srv.Name) || strings.EqualFold(server, srv.Slug) {
			return tbcplVidzeeSource{Lang: "Auto", Link: srv.Slug, Name: srv.Name}, true
		}
	}
	if strings.HasPrefix(strings.ToLower(server), tbcplDubServerPrefix) {
		if lang := tbcplDubLanguage(server[len(tbcplDubServerPrefix):]); lang != "" {
			slug := tbcplDubServerPrefix + lang
			return tbcplVidzeeSource{Lang: lang, Link: slug, Name: slug}, true
		}
	}
	return tbcplVidzeeSource{}, false
}

func (t *TBCPL) fetch(rawURL, referer, accept string) ([]byte, error) {
	return t.fetchCtx(context.Background(), rawURL, referer, accept)
}

// fetchCtx is fetch bounded by ctx. A non-2xx reply carries the body's
// {"error":...} message when there is one: Vidzee reports "No stream" and
// "Unknown server" that way, with 404 and 400.
func (t *TBCPL) fetchCtx(ctx context.Context, rawURL, referer, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("status %d for %s: %s", resp.StatusCode, rawURL, apiErr.Error)
		}
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

func parseTBCPLSeasonID(mediaID, seasonID string) (string, int, error) {
	tmdbID := extractTMDBID(mediaID)
	rawSeason := strings.TrimSpace(seasonID)
	if rawSeason == "" {
		rawSeason = "1"
	}

	if strings.Contains(rawSeason, ":") {
		parts := strings.Split(rawSeason, ":")
		if len(parts) >= 2 {
			if parts[0] != "" {
				tmdbID = parts[0]
			}
			rawSeason = parts[len(parts)-1]
		}
	}

	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", 0, err
	}
	season, err := strconv.Atoi(rawSeason)
	if err != nil || season <= 0 {
		return "", 0, fmt.Errorf("invalid season ID: %s", seasonID)
	}
	return tmdbID, season, nil
}

func parseTBCPLEpisodeID(mediaID, episodeID string) (string, int, int, error) {
	tmdbID := extractTMDBID(mediaID)
	raw := strings.TrimSpace(episodeID)
	if raw == "" {
		return "", 0, 0, fmt.Errorf("episode ID cannot be empty")
	}

	var seasonPart, episodePart string
	switch {
	case strings.Contains(raw, ":"):
		parts := strings.Split(raw, ":")
		if len(parts) < 3 {
			return "", 0, 0, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		tmdbID = parts[0]
		seasonPart = parts[len(parts)-2]
		episodePart = parts[len(parts)-1]
	case strings.Contains(raw, "."):
		parts := strings.SplitN(raw, ".", 2)
		seasonPart = parts[0]
		episodePart = parts[1]
	default:
		seasonPart = "1"
		episodePart = raw
	}

	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return "", 0, 0, err
	}
	season, err := strconv.Atoi(seasonPart)
	if err != nil || season <= 0 {
		return "", 0, 0, fmt.Errorf("invalid season in episode ID: %s", episodeID)
	}
	episode, err := strconv.Atoi(episodePart)
	if err != nil || episode <= 0 {
		return "", 0, 0, fmt.Errorf("invalid episode in episode ID: %s", episodeID)
	}
	return tmdbID, season, episode, nil
}

func formatTBCPLRating(rating float64) string {
	if rating <= 0 {
		return ""
	}
	out := strconv.FormatFloat(rating, 'f', 1, 64)
	return strings.TrimSuffix(out, ".0")
}
