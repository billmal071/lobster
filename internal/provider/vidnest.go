package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	vidnestAPIBase  = "https://new.vidnest.fun"
	vidnestOrigin   = "https://vidnest.fun"
	vidnestReferer  = "https://vidnest.fun/"
	vidnestAlphabet = "RB0fpH8ZEyVLkv7c2i6MAJ5u3IKFDxlS1NTsnGaqmXYdUrtzjwObCgQP94hoeW+/="
)

// vidnestBackend defines a VidNest upstream source.
type vidnestBackend struct {
	Name string // human-readable name
	Path string // URL path segment
	Type string // response type identifier
}

var vidnestBackends = []vidnestBackend{
	{Name: "MoviesAPI", Path: "moviesapi", Type: "alfa"},
	{Name: "HollyMovieHD", Path: "hollymoviehd", Type: "sigma"},
	{Name: "AllMovies", Path: "allmovies", Type: "lamda"},
	{Name: "VidLink", Path: "vidlink", Type: "hexa"},
	{Name: "KlikXXI", Path: "klikxxi", Type: "ophim"},
	{Name: "Movies4F", Path: "movies4f", Type: "catflix"},
}

// VidNest implements StreamProvider using vidnest.fun as a streaming aggregator.
// It proxies multiple backend sources and uses TMDB IDs for content lookup.
type VidNest struct {
	client *http.Client
}

// NewVidNest creates a new VidNest provider.
func NewVidNest() *VidNest {
	return &VidNest{
		client: httputil.NewClient(),
	}
}

// Search uses TMDB's free trending search endpoint (same as Soap2Day).
func (v *VidNest) Search(query string) ([]media.SearchResult, error) {
	searchURL := fmt.Sprintf("%s/search/trending?query=%s",
		tmdbSearchBase, url.QueryEscape(query))

	body, err := v.fetchTMDB(searchURL)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	var resp tmdbSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("search: parsing response: %w", err)
	}

	var results []media.SearchResult
	for _, raw := range resp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var item tmdbSearchResult
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.MediaType != "movie" && item.MediaType != "tv" {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}

		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:  item.displayTitle(),
			Type:   mt,
			Year:   item.year(),
			URL:    fmt.Sprintf("%s/%s/%d", tmdbSearchBase, item.MediaType, item.ID),
			Poster: tmdbPosterURL(item.PosterPath),
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no results found for %q", query)
	}
	return results, nil
}

// GetDetails returns minimal metadata (TMDB search provides most info).
func (v *VidNest) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}

// GetSeasons probes TMDB season endpoints to find available seasons.
func (v *VidNest) GetSeasons(id string) ([]media.Season, error) {
	tmdbID := extractTMDBID(id)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, err
	}

	var seasons []media.Season
	for n := 1; n <= 30; n++ {
		// Probe via VidNest's first backend to see if content exists.
		apiURL := fmt.Sprintf("%s/%s/tv/%s/%d/1",
			vidnestAPIBase, vidnestBackends[0].Path, tmdbID, n)
		body, err := v.fetchVidNest(apiURL)
		if err != nil {
			break
		}
		// Parse the encrypted wrapper.
		var wrapper struct {
			Data      string `json:"data"`
			Encrypted bool   `json:"encrypted"`
		}
		if json.Unmarshal(body, &wrapper) != nil || wrapper.Data == "" {
			break
		}
		decrypted, err := decryptVidNest(wrapper.Data)
		if err != nil || len(decrypted) == 0 {
			break
		}
		// Verify the decrypted response has actual stream data.
		if !vidnestHasStreams(decrypted) {
			break
		}
		seasons = append(seasons, media.Season{
			Number: n,
			ID:     fmt.Sprintf("%s:%d", tmdbID, n),
		})
	}

	if len(seasons) == 0 {
		return nil, fmt.Errorf("no seasons found")
	}
	return seasons, nil
}

// GetEpisodes returns a reasonable episode list for a season.
// VidNest backends accept any episode number, so we generate 1-50.
func (v *VidNest) GetEpisodes(id string, seasonID string) ([]media.Episode, error) {
	parts := strings.SplitN(seasonID, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid season ID: %s", seasonID)
	}
	tmdbID := parts[0]
	seasonNum, _ := strconv.Atoi(parts[1])

	episodes := make([]media.Episode, 0, 50)
	for ep := 1; ep <= 50; ep++ {
		episodes = append(episodes, media.Episode{
			Number: ep,
			Title:  fmt.Sprintf("Episode %d", ep),
			ID:     fmt.Sprintf("%s:%d:%d", tmdbID, seasonNum, ep),
		})
	}
	return episodes, nil
}

// GetServers returns one server per backend.
func (v *VidNest) GetServers(id string, episodeID string) ([]media.Server, error) {
	servers := make([]media.Server, 0, len(vidnestBackends))
	for _, b := range vidnestBackends {
		servers = append(servers, media.Server{
			Name: b.Name,
			ID:   b.Path,
		})
	}
	return servers, nil
}

// GetEmbedURL is not used; use Watch instead.
func (v *VidNest) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}

// Trending returns trending content from TMDB.
func (v *VidNest) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	mt := "movie"
	if mediaType == media.TV {
		mt = "tv"
	}
	return v.fetchTMDBTrending(mt)
}

// Recent returns recently added content (uses trending as proxy).
func (v *VidNest) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return v.Trending(mediaType)
}

// Watch tries multiple VidNest backends in parallel and returns the first
// successful stream. Uses a 20s overall timeout with 8s per backend.
func (v *VidNest) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	tmdbID := extractTMDBID(mediaID)
	if err := httputil.ValidateNumericID(tmdbID); err != nil {
		return nil, err
	}

	isTV := strings.HasPrefix(mediaID, "tv/")
	var season, episode int
	if episodeID != "" {
		isTV = true
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid episode ID: %s", episodeID)
		}
		tmdbID = parts[0]
		if err := httputil.ValidateNumericID(tmdbID); err != nil {
			return nil, fmt.Errorf("invalid TMDB ID in episode: %w", err)
		}
		season, _ = strconv.Atoi(parts[1])
		episode, _ = strconv.Atoi(parts[2])
		if season <= 0 || episode <= 0 {
			return nil, fmt.Errorf("invalid season/episode in episode ID: %s", episodeID)
		}
	}

	// If a specific server was requested, try only that backend.
	backends := vidnestBackends
	if server != "" && server != "default" {
		for _, b := range vidnestBackends {
			if strings.EqualFold(server, b.Path) || strings.EqualFold(server, b.Name) {
				backends = []vidnestBackend{b}
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	type result struct {
		stream *media.Stream
		err    error
	}
	ch := make(chan result, len(backends))

	var wg sync.WaitGroup
	for _, b := range backends {
		wg.Add(1)
		go func(backend vidnestBackend) {
			defer wg.Done()

			var apiURL string
			if isTV {
				apiURL = fmt.Sprintf("%s/%s/tv/%s/%d/%d",
					vidnestAPIBase, backend.Path, tmdbID, season, episode)
			} else {
				apiURL = fmt.Sprintf("%s/%s/movie/%s",
					vidnestAPIBase, backend.Path, tmdbID)
			}

			stream, err := v.resolveBackend(ctx, apiURL, backend, quality)
			ch <- result{stream, err}
		}(b)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var lastErr error
	for res := range ch {
		if res.err != nil {
			lastErr = res.err
			continue
		}
		// Verify the stream URL is accessible before returning.
		if err := v.verifyStream(ctx, res.stream); err != nil {
			lastErr = fmt.Errorf("stream unreachable: %w", err)
			continue
		}
		cancel()
		return res.stream, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("all backends failed")
	}
	return nil, fmt.Errorf("vidnest watch failed: %w", lastErr)
}

// verifyStream checks that a stream URL is accessible (rejects any HTTP error status).
func (v *VidNest) verifyStream(ctx context.Context, stream *media.Stream) error {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, stream.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if stream.Referer != "" {
		req.Header.Set("Referer", stream.Referer)
	}
	// Range request to avoid downloading the whole file
	req.Header.Set("Range", "bytes=0-1024")

	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, stream.URL)
	}
	return nil
}

// resolveBackend fetches and decrypts a stream from a single VidNest backend.
func (v *VidNest) resolveBackend(ctx context.Context, apiURL string, backend vidnestBackend, quality string) (*media.Stream, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("Origin", vidnestOrigin)
	req.Header.Set("Referer", vidnestReferer)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", backend.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: status %d", backend.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("%s: reading body: %w", backend.Name, err)
	}

	// Parse the encrypted wrapper.
	var wrapper struct {
		Data      string `json:"data"`
		Encrypted bool   `json:"encrypted"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("%s: parsing wrapper: %w", backend.Name, err)
	}
	if wrapper.Data == "" {
		return nil, fmt.Errorf("%s: empty data", backend.Name)
	}

	decrypted, err := decryptVidNest(wrapper.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: decryption: %w", backend.Name, err)
	}

	return v.parseBackendResponse(decrypted, backend, quality)
}

// parseBackendResponse extracts stream URLs from a decrypted backend response.
func (v *VidNest) parseBackendResponse(data []byte, backend vidnestBackend, quality string) (*media.Stream, error) {
	// Try sources-based format (moviesapi, hollymoviehd, vidlink, klikxxi).
	var sourcesResp struct {
		Sources []struct {
			URL     string `json:"url"`
			Quality string `json:"quality"`
			Type    string `json:"type"`
		} `json:"sources"`
	}
	if json.Unmarshal(data, &sourcesResp) == nil && len(sourcesResp.Sources) > 0 {
		for _, src := range sourcesResp.Sources {
			if src.URL != "" {
				return &media.Stream{
					URL:     src.URL,
					Quality: quality,
					Referer: vidnestReferer,
				}, nil
			}
		}
	}

	// Try streams-based format (allmovies).
	var streamsResp struct {
		Streams []struct {
			URL     string `json:"url"`
			Quality string `json:"quality"`
		} `json:"streams"`
	}
	if json.Unmarshal(data, &streamsResp) == nil && len(streamsResp.Streams) > 0 {
		for _, s := range streamsResp.Streams {
			if s.URL != "" {
				return &media.Stream{
					URL:     s.URL,
					Quality: quality,
					Referer: vidnestReferer,
				}, nil
			}
		}
	}

	// Try downloads-based format (movies4f).
	var downloadsResp struct {
		Code int `json:"code"`
		Data struct {
			Downloads []struct {
				URL        string `json:"url"`
				Resolution int    `json:"resolution"`
			} `json:"downloads"`
			Captions []struct {
				URL     string `json:"url"`
				Lan     string `json:"lan"`
				LanName string `json:"lanName"`
			} `json:"captions"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &downloadsResp) == nil && len(downloadsResp.Data.Downloads) > 0 {
		// Pick highest resolution.
		best := downloadsResp.Data.Downloads[0]
		for _, dl := range downloadsResp.Data.Downloads[1:] {
			if dl.Resolution > best.Resolution {
				best = dl
			}
		}
		if best.URL != "" {
			var subs []media.Subtitle
			for _, cap := range downloadsResp.Data.Captions {
				if cap.URL != "" {
					label := cap.LanName
					if label == "" {
						label = cap.Lan
					}
					subs = append(subs, media.Subtitle{
						Language: cap.Lan,
						Label:    label,
						URL:      cap.URL,
					})
				}
			}
			return &media.Stream{
				URL:       best.URL,
				Quality:   quality,
				Subtitles: subs,
				Referer:   vidnestReferer,
			}, nil
		}
	}

	return nil, fmt.Errorf("%s: no streams in response", backend.Name)
}

// decryptVidNest decodes VidNest's custom base64 encoding.
func decryptVidNest(data string) ([]byte, error) {
	lookup := make(map[byte]int, len(vidnestAlphabet))
	for i := 0; i < len(vidnestAlphabet); i++ {
		lookup[vidnestAlphabet[i]] = i
	}

	var result []byte
	for i := 0; i < len(data); i += 4 {
		chunk := data[i:]
		if len(chunk) > 4 {
			chunk = chunk[:4]
		}
		for len(chunk) < 4 {
			chunk += "="
		}

		vals := make([]int, 4)
		for j := 0; j < 4; j++ {
			if v, ok := lookup[chunk[j]]; ok {
				vals[j] = v
			} else {
				vals[j] = 64
			}
		}

		result = append(result, byte((vals[0]<<2)|(vals[1]>>4)))
		if vals[2] != 64 {
			result = append(result, byte(((vals[1]&15)<<4)|(vals[2]>>2)))
		}
		if vals[3] != 64 {
			result = append(result, byte(((vals[2]&3)<<6)|vals[3]))
		}
	}
	return result, nil
}

// vidnestHasStreams checks if decrypted JSON contains stream data.
func vidnestHasStreams(data []byte) bool {
	var probe struct {
		Sources []json.RawMessage `json:"sources"`
		Streams []json.RawMessage `json:"streams"`
		Data    json.RawMessage   `json:"data"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	return len(probe.Sources) > 0 || len(probe.Streams) > 0 || len(probe.Data) > 0
}

// fetchTMDB performs a GET request to TMDB with appropriate headers.
func (v *VidNest) fetchTMDB(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, text/html, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Referer", tmdbSearchBase+"/")

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

// fetchVidNest performs a GET request to VidNest's API with required headers.
func (v *VidNest) fetchVidNest(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Accept", "application/json, */*")
	req.Header.Set("Origin", vidnestOrigin)
	req.Header.Set("Referer", vidnestReferer)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d for %s", resp.StatusCode, rawURL)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
}

// fetchTMDBTrending returns trending results from TMDB filtered by media type.
func (v *VidNest) fetchTMDBTrending(mediaType string) ([]media.SearchResult, error) {
	trendingURL := fmt.Sprintf("%s/search/trending?query=a", tmdbSearchBase)

	body, err := v.fetchTMDB(trendingURL)
	if err != nil {
		return nil, fmt.Errorf("trending: %w", err)
	}

	var resp tmdbSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("trending: parsing response: %w", err)
	}

	var results []media.SearchResult
	for _, raw := range resp.Results {
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var item tmdbSearchResult
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.MediaType == "" {
			item.MediaType = mediaType
		}
		if item.MediaType != mediaType {
			continue
		}

		mt := media.Movie
		if item.MediaType == "tv" {
			mt = media.TV
		}

		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("%s/%d", item.MediaType, item.ID),
			Title:  item.displayTitle(),
			Type:   mt,
			Year:   item.year(),
			URL:    fmt.Sprintf("%s/%s/%d", tmdbSearchBase, item.MediaType, item.ID),
			Poster: tmdbPosterURL(item.PosterPath),
		})
	}
	return results, nil
}
