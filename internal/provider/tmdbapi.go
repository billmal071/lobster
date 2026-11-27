package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"lobster/internal/config"
	"lobster/internal/httputil"
)

const tmdbAPIMaxBody = 4 << 20

var (
	tmdbAPIBase        = "https://api.themoviedb.org/3"
	errTMDBKeyRejected = errors.New("TMDB API rejected the key in " + config.TMDBKeyEnv)
	errTMDBRateLimited = errors.New("TMDB API rate limit reached")
	tmdbAPIClient      = newTMDBAPIClient()

	tmdbIMDBLink   = regexp.MustCompile(`imdb\.com/title/tt(\d+)`)
	tmdbIMDBDigits = regexp.MustCompile(`^tt(\d+)$`)
)

func newTMDBAPIClient() *http.Client {
	c := httputil.NewClient()
	c.Timeout = 15 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

func tmdbSearchMulti(ctx context.Context, scrapeBase, query string, scrape func(rawURL string) ([]byte, error)) ([]byte, error) {
	if key := config.TMDBKeyFromEnv(); key != "" {
		return tmdbAPIGet(ctx, key, "/search/multi", url.Values{"query": {query}})
	}
	return scrape(tmdbMultiSearchURL(scrapeBase, query))
}

func tmdbTVIMDBID(ctx context.Context, scrapeBase string, tmdbID int, scrape func(rawURL string) ([]byte, error)) (string, error) {
	if key := config.TMDBKeyFromEnv(); key != "" {
		body, err := tmdbAPIGet(ctx, key, fmt.Sprintf("/tv/%d/external_ids", tmdbID), nil)
		if err != nil {
			return "", err
		}
		var ids struct {
			IMDBID string `json:"imdb_id"`
		}
		if err := json.Unmarshal(body, &ids); err != nil {
			return "", errors.New("parsing TMDB external ids failed")
		}
		if m := tmdbIMDBDigits.FindStringSubmatch(ids.IMDBID); m != nil {
			return m[1], nil
		}
		return "", nil
	}
	body, err := scrape(fmt.Sprintf("%s/tv/%d", strings.TrimRight(scrapeBase, "/"), tmdbID))
	if err != nil {
		return "", err
	}
	if m := tmdbIMDBLink.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	return "", nil
}

func tmdbAPIGet(ctx context.Context, key, path string, q url.Values) ([]byte, error) {
	if q == nil {
		q = url.Values{}
	}
	bearer := strings.HasPrefix(key, "eyJ")
	if !bearer {
		q.Set("api_key", key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tmdbAPIBase+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("building TMDB API request failed")
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := tmdbAPIClient.Do(req)
	if err != nil {
		return nil, tmdbAPIError("TMDB API request failed", err, key)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w (HTTP %d)", errTMDBKeyRejected, resp.StatusCode)
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w (HTTP %d)", errTMDBRateLimited, resp.StatusCode)
	default:
		return nil, fmt.Errorf("TMDB API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, tmdbAPIMaxBody+1))
	if err != nil {
		return nil, tmdbAPIError("reading TMDB API response failed", err, key)
	}
	if len(body) > tmdbAPIMaxBody {
		return nil, errors.New("TMDB API response too large")
	}
	return body, nil
}

func tmdbAPIError(what string, err error, key string) error {
	cause := httputil.CauseWithoutURL(err)
	msg := cause.Error()
	if strings.Contains(msg, key) || strings.Contains(msg, url.QueryEscape(key)) || strings.Contains(msg, "api_key") {
		return errors.New(what + ": details withheld")
	}
	return fmt.Errorf("%s: %w", what, cause)
}
