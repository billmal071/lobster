package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/extract"
	"lobster/internal/httputil"
	"lobster/internal/media"
)

// allanimeConfig holds the volatile AllAnime constants. They rotate upstream
// every few weeks; this is the single place to update them.
type allanimeConfig struct {
	apiBase       string
	clockBase     string
	refererSite   string
	sourcesOrigin string
	userAgent     string
	pqHash        string
	passphrase    string
	xorByte       byte
}

func defaultAllanimeConfig() allanimeConfig {
	return allanimeConfig{
		apiBase:       "https://api.allanime.day/api",
		clockBase:     "https://allanime.day",
		refererSite:   "https://allanime.to",
		sourcesOrigin: "https://youtu-chan.com",
		userAgent:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
		pqHash:        "d405d0edd690624b66baba3068e0edc3ac90f1597d898a1ec8db4e5c43c00fec",
		passphrase:    "Xot36i3lK3:v1",
		xorByte:       0x38,
	}
}

// httpDoer is the subset of *http.Client used here (swappable in tests).
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// AllAnime is a StreamProvider backed by the AllAnime GraphQL API.
type AllAnime struct {
	cfg    allanimeConfig
	client httpDoer
	trans  string // "sub" | "dub" | "raw"
}

// NewAllAnime constructs a provider defaulting to dub or sub.
func NewAllAnime(dub bool) *AllAnime {
	t := "sub"
	if dub {
		t = "dub"
	}
	return &AllAnime{cfg: defaultAllanimeConfig(), client: httputil.NewClient(), trans: t}
}

func (a *AllAnime) Translation() string     { return a.trans }
func (a *AllAnime) SetTranslation(t string) { a.trans = t }

// --- GraphQL query constants ---

const (
	allanimeSearchQuery   = `query($search: SearchInput, $limit: Int, $page: Int, $translationType: VaildTranslationTypeEnumType, $countryOrigin: VaildCountryOriginEnumType) { shows(search:$search, limit:$limit, page:$page, translationType:$translationType, countryOrigin:$countryOrigin) { edges { _id name englishName thumbnail availableEpisodes } } }`
	allanimeEpisodesQuery = `query($showId: String!) { show(_id:$showId) { _id availableEpisodesDetail } }`
)

// graphql POSTs a query+variables and decodes data into out.
func (a *AllAnime) graphql(query string, vars map[string]any, out any) error {
	payload, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequest(http.MethodPost, a.cfg.apiBase, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", a.cfg.userAgent)
	req.Header.Set("Referer", a.cfg.refererSite)
	req.Header.Set("Origin", a.cfg.refererSite)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("allanime: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (a *AllAnime) Search(query string) ([]media.SearchResult, error) {
	var r struct {
		Data struct {
			Shows struct {
				Edges []struct {
					ID                string         `json:"_id"`
					Name              string         `json:"name"`
					EnglishName       string         `json:"englishName"`
					Thumbnail         string         `json:"thumbnail"`
					AvailableEpisodes map[string]int `json:"availableEpisodes"`
				} `json:"edges"`
			} `json:"shows"`
		} `json:"data"`
	}
	vars := map[string]any{
		"search": map[string]any{"allowAdult": false, "allowUnknown": false, "query": query},
		"limit":  40, "page": 1, "translationType": a.trans, "countryOrigin": "ALL",
	}
	if err := a.graphql(allanimeSearchQuery, vars, &r); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	var out []media.SearchResult
	for _, e := range r.Data.Shows.Edges {
		title := e.EnglishName
		if title == "" {
			title = e.Name
		}
		out = append(out, media.SearchResult{
			ID: e.ID, Title: title, Type: media.TV, Poster: e.Thumbnail,
			Episodes: e.AvailableEpisodes[a.trans], URL: a.cfg.refererSite + "/anime/" + e.ID,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no anime found for %q", query)
	}
	return out, nil
}

// GetSeasons returns a single pseudo-season: AllAnime has no seasons.
func (a *AllAnime) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}

func (a *AllAnime) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	var r struct {
		Data struct {
			Show struct {
				Detail map[string][]string `json:"availableEpisodesDetail"`
			} `json:"show"`
		} `json:"data"`
	}
	if err := a.graphql(allanimeEpisodesQuery, map[string]any{"showId": id}, &r); err != nil {
		return nil, fmt.Errorf("episodes: %w", err)
	}
	list := r.Data.Show.Detail[a.trans] // strings, reverse order
	out := make([]media.Episode, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- { // ascending
		es := list[i]
		out = append(out, media.Episode{
			Number: epNumber(es, len(list)-i),
			Title:  "Episode " + es,
			ID:     encodeEpisodeID(id, es, a.trans),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s episodes", a.trans)
	}
	return out, nil
}

// epNumber parses the leading integer of an episodeString ("1","1.5","OVA1"),
// falling back to ordinal when non-numeric.
func epNumber(es string, ordinal int) int {
	end := 0
	for end < len(es) && es[end] >= '0' && es[end] <= '9' {
		end++
	}
	if end > 0 {
		if n, err := strconv.Atoi(es[:end]); err == nil {
			return n
		}
	}
	return ordinal
}

func (a *AllAnime) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	showID, episodeString, trans, ok := parseEpisodeID(episodeID)
	if !ok {
		return nil, fmt.Errorf("bad episode id %q", episodeID)
	}

	// 1) Persisted-query GET for the encrypted source list (Origin gates tobeparsed).
	vars, _ := json.Marshal(map[string]any{"showId": showID, "translationType": trans, "episodeString": episodeString})
	ext, _ := json.Marshal(map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": a.cfg.pqHash}})
	u := fmt.Sprintf("%s?variables=%s&extensions=%s", a.cfg.apiBase, url.QueryEscape(string(vars)), url.QueryEscape(string(ext)))
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", a.cfg.userAgent)
	req.Header.Set("Referer", a.cfg.sourcesOrigin)
	req.Header.Set("Origin", a.cfg.sourcesOrigin)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("allanime sources: status %d (origin gate?)", resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var sr struct {
		Data struct {
			Tobeparsed string `json:"tobeparsed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &sr); err != nil || sr.Data.Tobeparsed == "" {
		return nil, fmt.Errorf("no sources for %s ep %s", showID, episodeString)
	}

	// 2) Decrypt -> sourceUrls.
	plain, err := decryptTobeparsed(sr.Data.Tobeparsed, a.cfg.passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	var dec struct {
		Episode struct {
			SourceUrls []struct {
				SourceURL  string  `json:"sourceUrl"`
				SourceName string  `json:"sourceName"`
				Priority   float64 `json:"priority"`
			} `json:"sourceUrls"`
		} `json:"episode"`
	}
	if err := json.Unmarshal(plain, &dec); err != nil {
		return nil, fmt.Errorf("parse sources: %w", err)
	}
	srcs := dec.Episode.SourceUrls
	sort.SliceStable(srcs, func(i, j int) bool { return srcs[i].Priority > srcs[j].Priority })

	// 3) Try each candidate: internal CDN (--/clock.json) first, else embed extractor.
	var lastErr error
	for _, s := range srcs {
		if clockURL := decodeSourceURL(s.SourceURL, a.cfg.xorByte, a.cfg.clockBase); clockURL != "" {
			if st, err := a.resolveClock(clockURL); err == nil {
				return st, nil
			} else {
				lastErr = err
			}
			continue
		}
		// plain embed host -> reuse lobster's extractors
		ex, resolved := extract.ResolveForURL(s.SourceURL, a.cfg.refererSite)
		if st, err := ex.Extract(resolved, quality); err == nil && st != nil && st.URL != "" {
			return st, nil
		} else if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no playable source")
	}
	return nil, fmt.Errorf("allanime watch %s ep %s: %w", showID, episodeString, lastErr)
}

// resolveClock fetches a /clock.json document and maps it to a Stream.
func (a *AllAnime) resolveClock(clockURL string) (*media.Stream, error) {
	req, err := http.NewRequest(http.MethodGet, clockURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", a.cfg.userAgent)
	req.Header.Set("Referer", a.cfg.refererSite)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("clock.json: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var c struct {
		Links []struct {
			Link string `json:"link"`
			HLS  struct {
				URL string `json:"url"`
			} `json:"hls"`
		} `json:"links"`
		Subtitles []struct {
			Lang  string `json:"lang"`
			Label string `json:"label"`
			Src   string `json:"src"`
		} `json:"subtitles"`
		Referer string `json:"Referer"`
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, err
	}
	for _, l := range c.Links {
		u := l.Link
		if u == "" {
			u = l.HLS.URL
		}
		if u == "" {
			continue
		}
		st := &media.Stream{URL: u, Referer: c.Referer}
		for _, s := range c.Subtitles {
			if strings.EqualFold(s.Label, "Thumbnails") || strings.EqualFold(s.Lang, "Thumbnails") {
				continue
			}
			st.Subtitles = append(st.Subtitles, media.Subtitle{Language: s.Lang, Label: s.Label, URL: s.Src})
		}
		return st, nil
	}
	return nil, fmt.Errorf("clock.json had no playable link")
}

// Stable trivial methods.
func (a *AllAnime) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (a *AllAnime) GetServers(id, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AllAnime", ID: "default"}}, nil
}
func (a *AllAnime) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}
func (a *AllAnime) Trending(mt media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (a *AllAnime) Recent(mt media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
