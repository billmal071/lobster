// Package catalog reads lists and metadata from the official TMDB API
// (api.themoviedb.org/3) with a key the user supplies. It never resolves
// streams: a catalogue row is only a title to hand to the playback chain.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	apiBase          = "https://api.themoviedb.org/3"
	defaultImageBase = "https://image.tmdb.org/t/p/"
	posterSize       = "w342"
	maxBody          = 4 << 20
	cacheTTL         = 10 * time.Minute
	maxCacheEntries  = 128
	requestTimeout   = 10 * time.Second
)

var (
	ErrKeyRejected = errors.New("tmdb_api_key rejected")
	ErrRateLimited = errors.New("TMDB rate limit reached")

	idPattern = regexp.MustCompile(`^(movie|tv)/([0-9]+)$`)
)

type cacheEntry struct {
	body []byte
	at   time.Time
}

// Client is a TMDB API client. A nil *Client means no key is configured.
type Client struct {
	key     string
	base    string
	http    *http.Client
	now     func() time.Time
	mu      sync.Mutex
	cache   map[string]cacheEntry
	imgOnce sync.Once
	imgBase string
}

// New returns a client for the given key, or nil when the key is blank.
func New(key string) *Client {
	return NewWithBase(key, apiBase, httputil.NewClient())
}

// NewWithBase is New against another API root, for tests.
func NewWithBase(key, base string, hc *http.Client) *Client {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	cp := *hc
	cp.Timeout = requestTimeout
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
			return errors.New("redirect away from the TMDB API host refused")
		}
		return nil
	}
	return &Client{key: key, base: base, http: &cp, now: time.Now, cache: map[string]cacheEntry{}}
}

func (c *Client) isV4Token() bool { return strings.HasPrefix(c.key, "eyJ") }

func (c *Client) get(path string, q url.Values, check func([]byte) error) ([]byte, error) {
	cacheKey := path + "?" + q.Encode()
	c.mu.Lock()
	if e, ok := c.cache[cacheKey]; ok && c.now().Sub(e.at) < cacheTTL {
		c.mu.Unlock()
		return e.body, nil
	}
	c.mu.Unlock()

	full := url.Values{}
	for k, v := range q {
		full[k] = v
	}
	if !c.isV4Token() {
		full.Set("api_key", c.key)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.base+path+"?"+full.Encode(), nil)
	if err != nil {
		return nil, errors.New("building TMDB request failed")
	}
	req.Header.Set("Accept", "application/json")
	if c.isV4Token() {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TMDB request failed: %w", httputil.CauseWithoutURL(err))
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, ErrKeyRejected
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("TMDB returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("reading TMDB response: %w", httputil.CauseWithoutURL(err))
	}
	if len(body) > maxBody {
		return nil, errors.New("TMDB response too large")
	}
	if !json.Valid(body) {
		return nil, errors.New("TMDB returned malformed JSON")
	}
	if check != nil {
		if err := check(body); err != nil {
			return nil, err
		}
	}

	c.mu.Lock()
	now := c.now()
	for k, e := range c.cache {
		if now.Sub(e.at) >= cacheTTL {
			delete(c.cache, k)
		}
	}
	for len(c.cache) >= maxCacheEntries {
		oldest, first := "", true
		var oldestAt time.Time
		for k, e := range c.cache {
			if first || e.at.Before(oldestAt) {
				oldest, oldestAt, first = k, e.at, false
			}
		}
		delete(c.cache, oldest)
	}
	c.cache[cacheKey] = cacheEntry{body: body, at: now}
	c.mu.Unlock()
	return body, nil
}

func (c *Client) images() string {
	c.imgOnce.Do(func() {
		c.imgBase = defaultImageBase
		body, err := c.get("/configuration", nil, nil)
		if err != nil {
			return
		}
		var cfg struct {
			Images struct {
				SecureBaseURL string `json:"secure_base_url"`
			} `json:"images"`
		}
		if json.Unmarshal(body, &cfg) != nil {
			return
		}
		if b := cfg.Images.SecureBaseURL; strings.HasPrefix(b, "https://") {
			if !strings.HasSuffix(b, "/") {
				b += "/"
			}
			c.imgBase = b
		}
	})
	return c.imgBase
}

func (c *Client) list(path string, q url.Values, def media.MediaType) ([]media.SearchResult, error) {
	body, err := c.get(path, q, checkList)
	if err != nil {
		return nil, err
	}
	return decodeList(body, def, func() string { return c.images() })
}

func kind(mt media.MediaType) string {
	if mt == media.TV {
		return "tv"
	}
	return "movie"
}

// Trending is the week's trending titles of one type.
func (c *Client) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	return c.list("/trending/"+kind(mt)+"/week", nil, mt)
}

// Popular is the all-time popularity list of one type.
func (c *Client) Popular(mt media.MediaType) ([]media.SearchResult, error) {
	return c.list("/"+kind(mt)+"/popular", nil, mt)
}

// Recent is what is in cinemas (movies) or on the air (TV).
func (c *Client) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	if mt == media.TV {
		return c.list("/tv/on_the_air", nil, mt)
	}
	return c.list("/movie/now_playing", nil, mt)
}

// Anime is the popular Japanese-language animation list.
func (c *Client) Anime() ([]media.SearchResult, error) {
	q := url.Values{}
	q.Set("with_genres", "16")
	q.Set("with_original_language", "ja")
	q.Set("sort_by", "popularity.desc")
	return c.list("/discover/tv", q, media.TV)
}

// Search runs a multi search, keeping only movies and TV shows.
func (c *Client) Search(query string) ([]media.SearchResult, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	return c.list("/search/multi", q, media.Movie)
}

// Detail is ContentDetail plus the IMDb id TMDB links the title to.
type Detail struct {
	media.ContentDetail
	IMDbID string
}

func parseID(id string) (string, string, error) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return "", "", fmt.Errorf("not a TMDB id: %q", id)
	}
	return m[1], m[2], nil
}

// IsID reports whether id has the catalogue's movie/<n> or tv/<n> form.
func IsID(id string) bool {
	_, _, err := parseID(id)
	return err == nil
}

// Details fetches metadata for a movie/<n> or tv/<n> id.
func (c *Client) Details(id string) (*Detail, error) {
	k, n, err := parseID(id)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("append_to_response", "external_ids")
	body, err := c.get("/"+k+"/"+n, q, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Overview       string  `json:"overview"`
		VoteAverage    float64 `json:"vote_average"`
		Runtime        int     `json:"runtime"`
		EpisodeRuntime []int   `json:"episode_run_time"`
		ReleaseDate    string  `json:"release_date"`
		FirstAirDate   string  `json:"first_air_date"`
		Genres         []struct {
			Name string `json:"name"`
		} `json:"genres"`
		ProductionCountries []struct {
			Name string `json:"name"`
		} `json:"production_countries"`
		OriginCountry []string `json:"origin_country"`
		ExternalIDs   struct {
			IMDbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("decoding TMDB details failed")
	}
	d := &Detail{IMDbID: raw.ExternalIDs.IMDbID}
	d.Description = raw.Overview
	if raw.VoteAverage > 0 {
		d.Rating = strconv.FormatFloat(raw.VoteAverage, 'f', 1, 64)
	}
	runtime := raw.Runtime
	if runtime == 0 && len(raw.EpisodeRuntime) > 0 {
		runtime = raw.EpisodeRuntime[0]
	}
	if runtime > 0 {
		d.Duration = fmt.Sprintf("%d min", runtime)
	}
	d.Released = raw.ReleaseDate
	if d.Released == "" {
		d.Released = raw.FirstAirDate
	}
	for _, g := range raw.Genres {
		d.Genre = append(d.Genre, g.Name)
	}
	if len(raw.ProductionCountries) > 0 {
		d.Country = raw.ProductionCountries[0].Name
	} else {
		d.Country = strings.Join(raw.OriginCountry, ", ")
	}
	return d, nil
}

// Season lists a TV season's episodes.
func (c *Client) Season(id string, season int) ([]media.Episode, error) {
	k, n, err := parseID(id)
	if err != nil || k != "tv" {
		return nil, fmt.Errorf("not a TMDB tv id: %q", id)
	}
	if season < 1 {
		return nil, fmt.Errorf("invalid season %d", season)
	}
	body, err := c.get("/tv/"+n+"/season/"+strconv.Itoa(season), nil, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Episodes []struct {
			Number int    `json:"episode_number"`
			Name   string `json:"name"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("decoding TMDB season failed")
	}
	var eps []media.Episode
	for _, e := range raw.Episodes {
		if e.Number < 1 {
			continue
		}
		eps = append(eps, media.Episode{Number: e.Number, Title: e.Name, ID: strconv.Itoa(e.Number)})
	}
	return eps, nil
}

func listRows(body []byte) ([]json.RawMessage, error) {
	var env struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Results) == 0 {
		return nil, errors.New("decoding TMDB list failed")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(env.Results, &rows); err != nil {
		return nil, errors.New("decoding TMDB list failed")
	}
	return rows, nil
}

func checkList(body []byte) error {
	_, err := listRows(body)
	return err
}

func decodeList(body []byte, def media.MediaType, imageBase func() string) ([]media.SearchResult, error) {
	rows, err := listRows(body)
	if err != nil {
		return nil, err
	}
	var out []media.SearchResult
	seen := map[string]bool{}
	for _, rawRow := range rows {
		var row struct {
			ID           int    `json:"id"`
			MediaType    string `json:"media_type"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			PosterPath   string `json:"poster_path"`
		}
		if json.Unmarshal(rawRow, &row) != nil || row.ID <= 0 {
			continue
		}
		mt := def
		switch row.MediaType {
		case "":
		case "movie":
			mt = media.Movie
		case "tv":
			mt = media.TV
		default:
			continue
		}
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strings.TrimSpace(row.Name)
		}
		if title == "" {
			continue
		}
		id := fmt.Sprintf("%s/%d", kind(mt), row.ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		r := media.SearchResult{ID: id, Title: title, Type: mt, Year: year(row.ReleaseDate, row.FirstAirDate), FromCatalogue: true}
		if row.PosterPath != "" && imageBase != nil {
			r.Poster = imageBase() + posterSize + row.PosterPath
		}
		out = append(out, r)
	}
	return out, nil
}

func year(dates ...string) string {
	for _, d := range dates {
		if len(d) >= 4 {
			if _, err := strconv.Atoi(d[:4]); err == nil {
				return d[:4]
			}
		}
	}
	return ""
}
