package subtitle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// openSubtitlesAPI is a var, not a const, for the same reason as subdlAPI: a
// test needs a seam that does not reach the real service.
var openSubtitlesAPI = "https://api.opensubtitles.com/api/v1"

// OpenSubtitlesClient searches and downloads subtitles from OpenSubtitles.com.
type OpenSubtitlesClient struct {
	apiKey string
	client *http.Client
}

// NewOpenSubtitles creates a client for the OpenSubtitles.com REST API.
func NewOpenSubtitles(apiKey string) *OpenSubtitlesClient {
	return &OpenSubtitlesClient{
		apiKey: apiKey,
		client: httputil.NewClient(),
	}
}

type osSearchResponse struct {
	TotalCount int             `json:"total_count"`
	Data       []osSearchEntry `json:"data"`
}

type osSearchEntry struct {
	Attributes osAttributes `json:"attributes"`
}

type osAttributes struct {
	Language        string    `json:"language"`
	DownloadCount   int       `json:"download_count"`
	HearingImpaired bool      `json:"hearing_impaired"`
	FeatureDetails  osFeature `json:"feature_details"`
	Files           []osFile  `json:"files"`
}

// osFeature is the work a subtitle belongs to. Only the kind is read:
// "Movie" or "Episode".
type osFeature struct {
	FeatureType string `json:"feature_type"`
}

type osFile struct {
	FileID   int    `json:"file_id"`
	FileName string `json:"file_name"`
}

type osDownloadResponse struct {
	Link string `json:"link"`
}

// Search finds subtitles for a movie or TV episode.
func (o *OpenSubtitlesClient) Search(title, language string, season, episode int) ([]media.Subtitle, error) {
	params := url.Values{}
	params.Set("query", title)
	if language != "" {
		params.Set("languages", mapLanguageCode(language))
	}
	if season > 0 {
		params.Set("season_number", fmt.Sprintf("%d", season))
	}
	if episode > 0 {
		params.Set("episode_number", fmt.Sprintf("%d", episode))
	}

	reqURL := fmt.Sprintf("%s/subtitles?%s", openSubtitlesAPI, params.Encode())

	body, err := o.doRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("searching subtitles: %w", err)
	}

	var resp osSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	// Keep only entries belonging to the kind of work that was asked for.
	//
	// `query` is a fuzzy title search and nothing here constrains what comes
	// back: a film request sends no season or episode either, so episodes of a
	// same-named series are returned and were being handed to the player --
	// the same hazard that subtitled the 1996 film Fargo with the 2014
	// series' episode 1 through SubDL.
	//
	// This is enforced locally rather than by adding a `type` request
	// parameter: no OpenSubtitles key was available to verify the parameter's
	// accepted values against the live service, and an unverified filter on
	// the request risks turning working searches into empty ones. An entry
	// whose feature_type is absent or unrecognised is kept, so the check
	// rejects a stated mismatch and never a missing statement.
	wantEpisode := season > 0 || episode > 0

	var subtitles []media.Subtitle
	for _, entry := range resp.Data {
		if len(entry.Attributes.Files) == 0 {
			continue
		}
		switch strings.ToLower(entry.Attributes.FeatureDetails.FeatureType) {
		case "movie":
			if wantEpisode {
				continue
			}
		case "episode":
			if !wantEpisode {
				continue
			}
		}
		label := entry.Attributes.Language
		if entry.Attributes.HearingImpaired {
			label += " (SDH)"
		}
		subtitles = append(subtitles, media.Subtitle{
			Language: entry.Attributes.Language,
			Label:    label,
			// Store file ID in URL — resolved via ResolveDownloadURL()
			URL: fmt.Sprintf("opensubtitles:%d", entry.Attributes.Files[0].FileID),
		})
	}

	return subtitles, nil
}

// ResolveDownloadURL gets the actual download URL for a subtitle file ID.
func (o *OpenSubtitlesClient) ResolveDownloadURL(fileID int) (string, error) {
	reqURL := fmt.Sprintf("%s/download", openSubtitlesAPI)
	payload := fmt.Sprintf(`{"file_id":%d}`, fileID)

	body, err := o.doRequest("POST", reqURL, []byte(payload))
	if err != nil {
		return "", fmt.Errorf("requesting download: %w", err)
	}

	var resp osDownloadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parsing download response: %w", err)
	}

	if resp.Link == "" {
		return "", fmt.Errorf("no download link returned")
	}

	return resp.Link, nil
}

func (o *OpenSubtitlesClient) doRequest(method, reqURL string, payload []byte) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Api-Key", o.apiKey)
	req.Header.Set("User-Agent", "lobster v0.2.0")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024))
}

// mapLanguageCode maps common language names to ISO 639-1 codes.
func mapLanguageCode(lang string) string {
	codes := map[string]string{
		"english":    "en",
		"spanish":    "es",
		"french":     "fr",
		"german":     "de",
		"italian":    "it",
		"portuguese": "pt",
		"russian":    "ru",
		"japanese":   "ja",
		"korean":     "ko",
		"chinese":    "zh",
		"arabic":     "ar",
		"turkish":    "tr",
		"dutch":      "nl",
		"polish":     "pl",
		"swedish":    "sv",
		"norwegian":  "no",
		"danish":     "da",
		"finnish":    "fi",
		"greek":      "el",
		"czech":      "cs",
		"romanian":   "ro",
		"hungarian":  "hu",
		"hebrew":     "he",
		"thai":       "th",
		"indonesian": "id",
		"vietnamese": "vi",
		"hindi":      "hi",
	}

	if code, ok := codes[strings.ToLower(lang)]; ok {
		return code
	}
	if len(lang) == 2 {
		return strings.ToLower(lang)
	}
	return "en"
}
