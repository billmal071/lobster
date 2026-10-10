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
	// credentialsRejected records that OpenSubtitles answered /login by
	// refusing the credentials themselves. That verdict does not change
	// while the process keeps the same configuration, so the run stops
	// asking: wrong credentials cost one round trip, not one per episode.
	credentialsRejected bool
	// failedLogins counts logins that failed for a reason that may not
	// last -- a timeout, a 5xx, a transport error, a reply with no token.
	// Those are retried, up to maxLoginAttempts, so a service that was
	// briefly unavailable when the first episode started does not keep the
	// whole season on the anonymous quota.
	failedLogins int
	// downloadsSinceFailure counts download requests sent since the last
	// failed login. A retry waits for at least one, which is what stops a
	// burst of concurrent downloads from spending the whole retry budget
	// in the same instant -- and from stacking /login calls against a
	// documented limit of one per second.
	downloadsSinceFailure int
	// warnPending is the message TakeLoginWarning hands out once;
	// warnedRejected and warnedTransient keep each kind of degradation to a
	// single mention per client, so a season says it is on the anonymous
	// quota one time rather than once per episode.
	warnPending     string
	warnedRejected  bool
	warnedTransient bool
}

// maxLoginAttempts bounds how many times one run will try to log in after a
// login that failed transiently.
//
// A count, not a backoff: expiry and failure here are learned from the
// service, and this package deliberately has no clock -- a time-based retry
// would need an injectable now() and would push wall-clock waits into tests,
// which this repo does not allow. Three is picked against the cost of being
// wrong in either direction: the attempts are spaced by at least one real
// download each (see downloadsSinceFailure), so a blip that ends within the
// first few episodes is recovered from, while a service that is properly down
// costs three wasted round trips across the whole run instead of one per
// subtitle -- and a season stages up to three subtitles per episode.
const maxLoginAttempts = 3

// NewOpenSubtitles creates an anonymous client for the OpenSubtitles.com REST
// API: the API key identifies the application, and downloads are metered on
// the anonymous tier. Use OpenSubtitlesFor to raise that ceiling with an
// opensubtitles.com account.
func NewOpenSubtitles(apiKey string) *OpenSubtitlesClient {
	return newOpenSubtitlesWithLogin(apiKey, "", "")
}

// newOpenSubtitlesWithLogin creates a client that authenticates downloads with
// an opensubtitles.com account.
//
// Unexported on purpose: the login state that makes an account worth having
// only pays off when the client outlives the download, so callers outside
// this package go through OpenSubtitlesFor and cannot build a private client
// per episode.
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
func newOpenSubtitlesWithLogin(apiKey, username, password string) *OpenSubtitlesClient {
	return &OpenSubtitlesClient{
		apiKey:   apiKey,
		username: username,
		password: password,
		client:   httputil.NewClient(),
	}
}

// osCredentials identifies the account a client is logged in as. It is a map
// key and nothing more: it is never formatted, logged or put in an error, and
// OpenSubtitlesClient already holds the same three strings.
type osCredentials struct {
	apiKey   string
	username string
	password string
}

// sharedOpenSubtitles is the one client kept between downloads, with the
// credentials it was built for. One slot, not a map: a process has a single
// configuration, so capacity beyond one would only keep a second password
// alive for the lifetime of the process without ever being asked for it.
var sharedOpenSubtitles struct {
	mu     sync.Mutex
	creds  osCredentials
	client *OpenSubtitlesClient
}

// OpenSubtitlesFor returns the client to use for these credentials, reusing
// the one already built for them.
//
// The login state that makes the account worth having — the cached token, the
// verdict on the credentials, and what is left of the retry budget — lives on
// the client, so a fresh client per download throws all of it away. A season then logs in once per episode
// instead of once, and, worse, a wrong or expired credential pair costs a
// refused round trip on every episode rather than on the first.
//
// Keyed on the credentials rather than remembered once, because cfg is a
// global that can change — the tests in this repo mutate and restore it — and
// a client retained regardless of what the credentials now say would
// authenticate as an account the run is no longer configured for.
func OpenSubtitlesFor(apiKey, username, password string) *OpenSubtitlesClient {
	creds := osCredentials{apiKey: apiKey, username: username, password: password}

	sharedOpenSubtitles.mu.Lock()
	defer sharedOpenSubtitles.mu.Unlock()

	if sharedOpenSubtitles.client != nil && sharedOpenSubtitles.creds == creds {
		return sharedOpenSubtitles.client
	}
	client := newOpenSubtitlesWithLogin(apiKey, username, password)
	sharedOpenSubtitles.creds = creds
	sharedOpenSubtitles.client = client
	return client
}

// bearerToken returns the token to authenticate the next download with, "" for
// an anonymous request.
func (o *OpenSubtitlesClient) bearerToken() string {
	return o.bearer("", false)
}

// bearerTokenAfter returns the token to retry a download with after the
// request that carried `rejected` was answered with a 401.
//
// The rejected token is passed in rather than implied, because the client is
// shared by every download in the run: if several downloads are in flight when
// one token expires, each of them is refused, and each would otherwise clear
// and replace a token another one has already replaced. One expiry would then
// cost one /login per download in flight, against a documented limit of one
// login per second -- turning a recoverable expiry into a refusal. Knowing
// which token was refused is what lets all but the first caller take the
// replacement instead of asking for another.
func (o *OpenSubtitlesClient) bearerTokenAfter(rejected string) string {
	return o.bearer(rejected, true)
}

// bearer is the whole login state machine, under o.mu.
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
func (o *OpenSubtitlesClient) bearer(rejected string, refresh bool) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch {
	case o.username == "" || o.password == "":
		return ""
	case refresh:
		// Somebody else may already have replaced the token this caller
		// was refused on; take theirs rather than logging in again.
		if o.token != "" && o.token != rejected {
			return o.token
		}
		o.token = ""
	case o.token != "":
		return o.token
	}
	if o.credentialsRejected || !o.mayAttemptLogin() {
		return ""
	}

	payload, err := json.Marshal(map[string]string{
		"username": o.username,
		"password": o.password,
	})
	if err != nil {
		o.recordLoginFailure(err)
		return ""
	}
	body, err := o.doRequest("POST", openSubtitlesAPI+"/login", payload, "")
	if err != nil {
		o.recordLoginFailure(err)
		return ""
	}
	var resp osLoginResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		o.recordLoginFailure(fmt.Errorf("unreadable login reply: %w", err))
		return ""
	}
	if resp.Token == "" {
		o.recordLoginFailure(errors.New("login reply carried no token"))
		return ""
	}
	o.token = resp.Token
	return o.token
}

// mayAttemptLogin reports whether a login is worth another round trip.
// o.mu must be held.
//
// The first attempt is always allowed. After a transient failure the run gets
// maxLoginAttempts in total, and each retry has to wait for a download request
// to have gone out since the last failure. That spacing is what makes the
// budget a property of the run rather than of how many downloads happen to be
// in flight: a burst of concurrent downloads that all find no token spends one
// attempt between them, not three.
func (o *OpenSubtitlesClient) mayAttemptLogin() bool {
	switch {
	case o.failedLogins == 0:
		return true
	case o.failedLogins >= maxLoginAttempts:
		return false
	default:
		return o.downloadsSinceFailure > 0
	}
}

// recordLoginFailure classifies a failed login. o.mu must be held.
//
// 401 and 403 are the service stating that the sign-in itself is not
// acceptable -- wrong username or password, or an API key it will not take.
// Nothing this run can do changes that answer, so it is recorded as permanent
// and never retried, which is the behaviour the per-episode round trip was
// removed for in the first place.
//
// Everything else is the service failing to answer rather than answering
// "no": a timeout, a transport error, a 5xx, a 429 from the one-login-per-
// second limit, a reply that is not the documented shape. Those can stop being
// true a few seconds later, so they get a bounded retry instead of ending the
// run's chance of ever being signed in.
//
// Which codes a live opensubtitles.com actually returns for bad credentials
// has not been verified from this machine -- there is no key here. 401 is what
// the published docs and the stub model; 403 is included because the same API
// uses it for a key it will not accept, which is equally permanent. If the
// live service were to answer a wrong password with, say, a 500, the only cost
// is maxLoginAttempts round trips instead of one.
func (o *OpenSubtitlesClient) recordLoginFailure(err error) {
	var status *osStatusError
	if errors.As(err, &status) && (status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden) {
		o.credentialsRejected = true
		o.noteDegraded(&o.warnedRejected, fmt.Sprintf(
			"OpenSubtitles rejected the sign-in (HTTP %d), so subtitles are being downloaded on the smaller anonymous quota for the rest of this run; check opensubtitles_username, opensubtitles_password and opensubtitles_api_key",
			status.Code))
		return
	}
	o.failedLogins++
	o.downloadsSinceFailure = 0
	o.noteDegraded(&o.warnedTransient, fmt.Sprintf(
		"could not sign in to OpenSubtitles (%v), so subtitles are being downloaded on the smaller anonymous quota; the sign-in will be tried again on a later download",
		err))
}

// noteDownloadAttempt records that a download request has gone out, which is
// what lets a transient login failure be retried later in the run.
func (o *OpenSubtitlesClient) noteDownloadAttempt() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.downloadsSinceFailure++
}

// noteDegraded queues the one-shot message for a kind of degradation.
// o.mu must be held.
func (o *OpenSubtitlesClient) noteDegraded(warned *bool, msg string) {
	if *warned {
		return
	}
	*warned = true
	o.warnPending = msg
}

// TakeLoginWarning returns a message about having dropped to the anonymous
// download quota, or "" when there is nothing new to say, and clears it.
//
// It exists because the degradation is otherwise silent: a run whose sign-in
// failed keeps working, on a quota small enough that a season stops part way
// through with a bare HTTP 403 that reads like "no key configured". The
// wording is left to the caller to print because this package has no output of
// its own, and each kind of failure is offered once per client so a season
// says it one time rather than once per episode.
//
// It claims only what the client observed: that a sign-in was refused or could
// not be completed, and that downloads are therefore anonymous. It does not
// name the quota numbers, which are documented but have not been exercised
// against the live service from here.
func (o *OpenSubtitlesClient) TakeLoginWarning() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	msg := o.warnPending
	o.warnPending = ""
	return msg
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

	tok := o.bearerToken()
	body, err := o.doRequest("POST", reqURL, []byte(payload), tok)
	o.noteDownloadAttempt()
	// A 401 on a request that carried a token means the token is no longer
	// good -- it expired, or the session was ended elsewhere. One fresh login
	// and one retry; a 401 that survives that is a real failure and is
	// reported, so this cannot loop.
	var status *osStatusError
	if errors.As(err, &status) && status.Code == http.StatusUnauthorized {
		if fresh := o.bearerTokenAfter(tok); fresh != "" {
			body, err = o.doRequest("POST", reqURL, []byte(payload), fresh)
			o.noteDownloadAttempt()
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
