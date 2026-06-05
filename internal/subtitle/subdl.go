package subtitle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const subdlAPI = "https://api.subdl.com/api/v1/subtitles"

// SubDLClient searches and downloads subtitles from subdl.com.
type SubDLClient struct {
	apiKey string
	client *http.Client
}

// NewSubDL creates a client for the SubDL REST API.
func NewSubDL(apiKey string) *SubDLClient {
	return &SubDLClient{
		apiKey: apiKey,
		client: httputil.NewClient(),
	}
}

type subdlResponse struct {
	Status    bool            `json:"status"`
	Results   []subdlResult   `json:"results"`
	Subtitles []subdlSubtitle `json:"subtitles"`
}

type subdlResult struct {
	SDId   int    `json:"sd_id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	ImdbID string `json:"imdb_id"`
	TmdbID int    `json:"tmdb_id"`
	Year   int    `json:"year"`
}

type subdlSubtitle struct {
	ReleaseName  string `json:"release_name"`
	Language     string `json:"lang"`
	Author       string `json:"author"`
	URL          string `json:"url"`
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	HI           bool   `json:"hi"`
	FullSeason   bool   `json:"full_season"`
}

// Search finds subtitles for a movie or TV episode.
// SubDL requires two steps: first search by film_name to get sd_id,
// then fetch subtitles by sd_id.
func (s *SubDLClient) Search(title, language string, season, episode int) ([]media.Subtitle, error) {
	// Step 1: Search for the film to get sd_id.
	searchParams := url.Values{}
	searchParams.Set("api_key", s.apiKey)
	searchParams.Set("film_name", title)
	searchParams.Set("subs_per_page", "10")

	searchResp, err := s.fetchAPI(searchParams)
	if err != nil {
		return nil, err
	}

	if len(searchResp.Results) == 0 {
		return nil, fmt.Errorf("no results for %q", title)
	}

	// Pick the best matching result (prefer exact type match).
	sdID := searchResp.Results[0].SDId
	wantType := "movie"
	if season > 0 || episode > 0 {
		wantType = "tv"
	}
	for _, r := range searchResp.Results {
		if r.Type == wantType {
			sdID = r.SDId
			break
		}
	}

	// Step 2: Fetch subtitles by sd_id.
	subParams := url.Values{}
	subParams.Set("api_key", s.apiKey)
	subParams.Set("sd_id", fmt.Sprintf("%d", sdID))
	if language != "" {
		subParams.Set("languages", mapLanguageCode(language))
	}
	if season > 0 {
		subParams.Set("season_number", fmt.Sprintf("%d", season))
	}
	if episode > 0 {
		subParams.Set("episode_number", fmt.Sprintf("%d", episode))
	}
	subParams.Set("subs_per_page", "10")

	subResp, err := s.fetchAPI(subParams)
	if err != nil {
		return nil, err
	}

	var subtitles []media.Subtitle
	for _, sub := range subResp.Subtitles {
		if sub.URL == "" {
			continue
		}
		// SubDL API doesn't always filter by episode server-side.
		// Filter client-side to ensure we get the right episode.
		if episode > 0 && sub.Episode > 0 && sub.Episode != episode {
			continue
		}
		if season > 0 && sub.Season > 0 && sub.Season != season {
			continue
		}
		// Skip full-season packs (contain multiple episodes in one zip)
		if episode > 0 && sub.FullSeason {
			continue
		}
		// Check release name for wrong episode markers
		if episode > 0 && isWrongEpisode(strings.ToLower(sub.ReleaseName), season, episode) {
			continue
		}
		label := sub.Language
		if sub.HI {
			label += " (SDH)"
		}
		dlURL := sub.URL
		if strings.HasPrefix(dlURL, "/") {
			dlURL = "https://dl.subdl.com" + dlURL
		}
		subtitles = append(subtitles, media.Subtitle{
			Language: sub.Language,
			Label:    label,
			URL:      "subdl:" + dlURL,
		})
	}

	return subtitles, nil
}

// fetchAPI makes a GET request to the SubDL API and returns the parsed response.
func (s *SubDLClient) fetchAPI(params url.Values) (*subdlResponse, error) {
	reqURL := fmt.Sprintf("%s?%s", subdlAPI, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lobster/0.6")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SubDL request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SubDL API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result subdlResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &result, nil
}

// DownloadAndExtract downloads a SubDL zip file and extracts the first SRT/VTT subtitle.
func (s *SubDLClient) DownloadAndExtract(zipURL string, tmpDir *TempDir) (string, error) {
	req, err := http.NewRequest("GET", zipURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "lobster v0.3.1")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading subtitle zip: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("subtitle download returned status %d", resp.StatusCode)
	}

	// Read zip into memory (limit 10MB)
	zipData, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading zip: %w", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("opening zip: %w", err)
	}

	// Find first SRT or VTT file
	var best *zip.File
	for _, f := range reader.File {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext == ".srt" || ext == ".vtt" || ext == ".ass" {
			best = f
			if ext == ".srt" {
				break // prefer SRT
			}
		}
	}

	if best == nil {
		return "", fmt.Errorf("no subtitle file found in zip")
	}

	rc, err := best.Open()
	if err != nil {
		return "", fmt.Errorf("opening subtitle in zip: %w", err)
	}
	defer rc.Close()

	localPath := filepath.Join(tmpDir.path, httputil.SanitizeFilename(filepath.Base(best.Name)))
	out, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("creating subtitle file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, io.LimitReader(rc, 10*1024*1024)); err != nil {
		return "", fmt.Errorf("extracting subtitle: %w", err)
	}

	return localPath, nil
}
