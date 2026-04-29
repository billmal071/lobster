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
func (s *SubDLClient) Search(title, language string, season, episode int) ([]media.Subtitle, error) {
	params := url.Values{}
	params.Set("api_key", s.apiKey)
	params.Set("film_name", title)
	if language != "" {
		params.Set("languages", mapLanguageCode(language))
	}
	if season > 0 {
		params.Set("season_number", fmt.Sprintf("%d", season))
	}
	if episode > 0 {
		params.Set("episode_number", fmt.Sprintf("%d", episode))
	}
	params.Set("subs_per_page", "10")

	reqURL := fmt.Sprintf("%s?%s", subdlAPI, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lobster v0.3.1")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching subtitles: %w", err)
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

	var subtitles []media.Subtitle
	for _, sub := range result.Subtitles {
		if sub.URL == "" {
			continue
		}
		label := sub.Language
		if sub.HI {
			label += " (SDH)"
		}
		// SubDL returns relative paths like /subtitles/filename.zip
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
