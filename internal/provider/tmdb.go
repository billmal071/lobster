package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"lobster/internal/httputil"
)

// --- TMDB shared const, types, and helpers ---

const tmdbSearchBase = "https://www.themoviedb.org"

type tmdbSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tmdbSearchResult struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`      // movie
	Name         string  `json:"name"`       // tv
	MediaType    string  `json:"media_type"` // "movie" or "tv"
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`   // movie
	FirstAirDate string  `json:"first_air_date"` // tv
	VoteAverage  float64 `json:"vote_average"`
	PosterPath   string  `json:"poster_path"`
}

func (r *tmdbSearchResult) displayTitle() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Title
}

func (r *tmdbSearchResult) year() string {
	date := r.ReleaseDate
	if date == "" {
		date = r.FirstAirDate
	}
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// tmdbPosterURL builds a full TMDB poster image URL from a poster path.
func tmdbPosterURL(posterPath string) string {
	if posterPath == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/w500" + posterPath
}

// extractTMDBID extracts the numeric TMDB ID from a provider ID like "tv/79744" or "movie/299534".
func extractTMDBID(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

// --- Confident-match poster selection ---

// pickPoster returns the TMDB poster URL of the first CONFIDENT match for the
// given title/year/type, or "" if none (caller keeps its existing poster).
func pickPoster(results []tmdbSearchResult, title, year string, isTV bool) string {
	wantType := "movie"
	if isTV {
		wantType = "tv"
	}
	for i := range results {
		r := &results[i]
		if r.MediaType != wantType {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(r.displayTitle()), strings.TrimSpace(title)) {
			continue
		}
		if !yearMatches(r.year(), year) {
			continue
		}
		if r.PosterPath == "" {
			continue
		}
		return tmdbPosterURL(r.PosterPath)
	}
	return ""
}

// yearMatches is lenient when either year is unknown/unparseable, else requires
// the years within ±1 (release vs production year commonly differ by one).
func yearMatches(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	ai, err1 := strconv.Atoi(a)
	bi, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil {
		return true
	}
	d := ai - bi
	if d < 0 {
		d = -d
	}
	return d <= 1
}

// --- TMDBPoster: fetch + parse + memoize ---

var (
	tmdbBaseURL    = tmdbSearchBase // overridable in tests
	tmdbClient     = httputil.NewClient()
	tmdbPosterMemo sync.Map // "title|year|isTV" -> string (incl. "" misses)
)

// TMDBPoster returns a high-res TMDB poster URL for a title, or "" if there is
// no confident match. Memoized per (title|year|isTV) for the process, including
// negative results so a miss is never re-queried.
func TMDBPoster(title, year string, isTV bool) string {
	key := fmt.Sprintf("%s|%s|%t", strings.ToLower(strings.TrimSpace(title)), strings.TrimSpace(year), isTV)
	if v, ok := tmdbPosterMemo.Load(key); ok {
		return v.(string)
	}
	res, cacheable := tmdbPosterLookup(title, year, isTV)
	if cacheable {
		// Only memoize deterministic outcomes (a parsed response — match or not).
		// Transient errors are NOT cached, so a momentary TMDB blip doesn't stick
		// a permanent miss for this title for the rest of the session.
		tmdbPosterMemo.Store(key, res)
	}
	return res
}

// tmdbPosterLookup returns the matched poster URL (or "" for a genuine no-match)
// and whether the outcome is cacheable. A network/parse error returns ("", false)
// so the caller retries on the next selection instead of caching the failure.
func tmdbPosterLookup(title, year string, isTV bool) (string, bool) {
	endpoint := fmt.Sprintf("%s/search/trending?query=%s",
		strings.TrimRight(tmdbBaseURL, "/"), url.QueryEscape(title))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", strings.TrimRight(tmdbBaseURL, "/")+"/")
	resp, err := tmdbClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", false // non-2xx (rate-limit/proxy error): transient, don't cache
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	var sr tmdbSearchResponse
	if json.Unmarshal(data, &sr) != nil {
		return "", false
	}
	objs := make([]tmdbSearchResult, 0, len(sr.Results))
	for _, raw := range sr.Results {
		if len(raw) == 0 || raw[0] != '{' { // skip autocomplete <span> strings
			continue
		}
		var item tmdbSearchResult
		if json.Unmarshal(raw, &item) == nil {
			objs = append(objs, item)
		}
	}
	return pickPoster(objs, title, year, isTV), true
}
