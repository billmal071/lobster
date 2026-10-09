package subtitle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// openSubtitlesAPI is a var, not a const, for the same reason as subdlAPI: a
// test needs a seam that does not reach the real service.
var openSubtitlesAPI = "https://api.opensubtitles.com/api/v1"

// OpenSubtitlesClient searches and downloads subtitles from OpenSubtitles.com.
type OpenSubtitlesClient struct {
	apiKey   string
	username string
	password string
	client   *http.Client

	// mu guards the login state below. A client is reused across the
	// episodes of a season, so two downloads can want a token at once; the
	// lock is held across the /login round trip deliberately, so they wait
	// for one login instead of racing two.
	mu sync.Mutex
	// token is the bearer from the last successful /login, "" when none.
	token string
	// loginFailed records that a login was tried and did not work, so the
	// run stops paying a round trip per episode for credentials that are
	// wrong or for a service that is refusing logins.
	loginFailed bool
}

// NewOpenSubtitles creates an anonymous client for the OpenSubtitles.com REST
// API: the API key identifies the application, and downloads are metered on
// the anonymous tier. Use NewOpenSubtitlesWithLogin to raise that ceiling.
func NewOpenSubtitles(apiKey string) *OpenSubtitlesClient {
	return NewOpenSubtitlesWithLogin(apiKey, "", "")
}

// NewOpenSubtitlesWithLogin creates a client that authenticates downloads with
// an opensubtitles.com account.
//
// OpenSubtitles meters downloads, not searches, and it meters them by whether
// the request carries a user token: an API key alone gets 5 downloads per 24h,
// the same key plus a logged-in free account gets 20. Five is less than half
// a season, and running out surfaces as a bare HTTP 403 that reads exactly
// like "no key configured" — so the quota is not a detail the user can
// diagnose from the outside.
//
// Empty username or password means "stay anonymous", which is the default and
// is never an error: the account is strictly an optional upgrade, and a client
// without one behaves as it always did.
func NewOpenSubtitlesWithLogin(apiKey, username, password string) *OpenSubtitlesClient {
	return &OpenSubtitlesClient{
		apiKey:   apiKey,
		username: username,
		password: password,
		client:   httputil.NewClient(),
	}
}

// bearerToken returns the token to authenticate the next download with, "" for
// an anonymous request.
//
// It logs in lazily, and only ever from the download path. Logging in eagerly
// in the constructor would put a round trip in front of every search, and
// searching is both free and unmetered — `find` and `episodes` never download
// anything, so they would pay for a quota they cannot spend, on commands this
// project requires to stay bounded. The download path already waits on a
// request of its own, so the one login it adds (once per client, not once per
// episode) is the cheapest place to put it.
//
// Every failure here returns "" rather than an error: losing the 20/day tier
// back down to 5/day is much better than losing subtitles, so a refused or
// unreachable login degrades to the anonymous request instead of failing the
// download.
//
// refresh asks for a new token even when one is cached, which is how an
// expired token is replaced — OpenSubtitles tokens are documented as lasting
// roughly a day, longer than any single lobster run, so there is no clock
// here: expiry is learned from the service rejecting it, not predicted.
func (o *OpenSubtitlesClient) bearerToken(refresh bool) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.username == "" || o.password == "" {
		return ""
	}
	if o.token != "" && !refresh {
		return o.token
	}
	if refresh {
		o.token = ""
	}
	if o.loginFailed {
		return ""
	}

	payload, err := json.Marshal(map[string]string{
		"username": o.username,
		"password": o.password,
	})
	if err != nil {
		o.loginFailed = true
		return ""
	}
	body, err := o.doRequest("POST", openSubtitlesAPI+"/login", payload, "")
	if err != nil {
		o.loginFailed = true
		return ""
	}
	var resp osLoginResponse
	if err := json.Unmarshal(body, &resp); err != nil || resp.Token == "" {
		o.loginFailed = true
		return ""
	}
	o.token = resp.Token
	return o.token
}

// osLoginResponse is POST /login's reply.
//
// base_url is deliberately not read. The documented field names an account's
// preferred API host, and honouring it would mean sending every later request
// somewhere this change cannot verify — there is no key on hand to exercise
// the live v1 service with. Ignoring it keeps requests going to the host that
// already works; the cost is at most not using a VIP endpoint.
type osLoginResponse struct {
	Token string `json:"token"`
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

// osStatusError is a non-200 from the API, carrying the code so the download
// path can tell a rejected token (401) from every other refusal.
type osStatusError struct{ Code int }

func (e *osStatusError) Error() string {
	return fmt.Sprintf("API returned status %d", e.Code)
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

	// No bearer: a search spends no download quota, so it is not worth a
	// login (see bearerToken).
	body, err := o.doRequest("GET", reqURL, nil, "")
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
//
// This is the only call that spends quota, so it is the only one that logs in.
func (o *OpenSubtitlesClient) ResolveDownloadURL(fileID int) (string, error) {
	reqURL := fmt.Sprintf("%s/download", openSubtitlesAPI)
	payload := fmt.Sprintf(`{"file_id":%d}`, fileID)

	body, err := o.doRequest("POST", reqURL, []byte(payload), o.bearerToken(false))
	// A 401 on a request that carried a token means the token is no longer
	// good -- it expired, or the session was ended elsewhere. One fresh login
	// and one retry; a 401 that survives that is a real failure and is
	// reported, so this cannot loop.
	var status *osStatusError
	if errors.As(err, &status) && status.Code == http.StatusUnauthorized {
		if tok := o.bearerToken(true); tok != "" {
			body, err = o.doRequest("POST", reqURL, []byte(payload), tok)
		}
	}
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

func (o *OpenSubtitlesClient) doRequest(method, reqURL string, payload []byte, bearer string) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Api-Key", o.apiKey)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
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
		return nil, &osStatusError{Code: resp.StatusCode}
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
