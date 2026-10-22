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
	"path"
	"path/filepath"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// subdlAPI is a var, not a const, so a test can point the two-step protocol at
// an httptest server. Nothing in the package reassigns it.
var subdlAPI = "https://api.subdl.com/api/v1/subtitles"

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
	ReleaseName string `json:"release_name"`
	Language    string `json:"lang"`
	Author      string `json:"author"`
	URL         string `json:"url"`
	Season      int    `json:"season"`
	Episode     int    `json:"episode"`
	HI          bool   `json:"hi"`
	FullSeason  bool   `json:"full_season"`
}

// Search finds subtitles for a movie or TV episode.
// SubDL requires two steps: first search by film_name to get sd_id,
// then fetch subtitles by sd_id.
func (s *SubDLClient) Search(title, language string, season, episode int) ([]media.Subtitle, error) {
	// Which kind of work this is. The caller asking for a season or an episode
	// is the only signal there is, and it is a reliable one: a film request
	// carries neither.
	wantType := "movie"
	if season > 0 || episode > 0 {
		wantType = "tv"
	}

	// Step 1: Search for the film to get sd_id.
	//
	// `type` matters more than it looks. SubDL indexes films and series
	// separately and answers a bare `film_name` with exactly one result -- its
	// choice of work, not the caller's -- so the kind has to be asked for.
	// Measured 2026-10-05: `Fargo` alone is the 2014 series (sd_id 1300401),
	// `Fargo` with type=movie is the 1996 film (213795); `Avatar` alone is the
	// 2009 film, with type=tv it is Avatar: The Last Airbender; `Bleach` alone
	// is a 2026 film, with type=tv it is the 2004 series.
	//
	// It narrows the kind without narrowing the title matching: every alias
	// from the AnimeOnsen work still resolves with it, including both
	// spellings of "KAMUI: He's Behind You" / "Ushiro no Shoumen Kamui-san".
	searchParams := url.Values{}
	searchParams.Set("api_key", s.apiKey)
	searchParams.Set("film_name", title)
	searchParams.Set("type", wantType)
	searchParams.Set("subs_per_page", "10")

	searchResp, err := s.fetchAPI(searchParams)
	if err != nil {
		return nil, err
	}

	if len(searchResp.Results) == 0 {
		return nil, fmt.Errorf("no results for %q", title)
	}

	// Take only a result SubDL itself labels the kind that was asked for.
	//
	// Falling back to Results[0] regardless of type is what subtitled a film
	// with a series. A film request has season == 0 and episode == 0, so no
	// season or episode parameter is sent, and *every* client-side filter
	// below is gated on one of those being non-zero -- the episode check, the
	// season check and isWrongEpisode are all inert.
	// A series sd_id answers that unfiltered query with whatever it has, so
	// `play` on the 1996 film Fargo took "Fargo - 1x01 - The Crocodile's
	// Dilemma" (measured against sd_id 1300401 on 2026-10-05).
	//
	// The name is deliberately not compared. It would not help -- every wrong
	// match found carried the *same* name as the query (Fargo, Avatar, Dark,
	// Monster) -- and it would hurt, because SubDL resolves aliases: both
	// "KAMUI: He's Behind You" and "Ushiro no Shoumen Kamui-san" return the
	// one correct sd_id under a name neither query spelled. Likewise the year,
	// which this endpoint ignores outright (film_name=Avatar&year=2005 still
	// answers with the 2009 film).
	//
	// A result carrying no type is accepted, but only as a fallback: the check
	// rejects a stated mismatch, never a missing statement, and an explicit
	// match is still the better answer. Taking the first result that is either
	// untyped or matching would let the order SubDL happens to return things in
	// decide which sd_id step 2 pivots on, and SubDL documents no ordering.
	//
	// Neither half of that has been observed. Across 57 live step-1 queries on
	// 2026-10-06 every answer carried 0 or 1 results -- with and without
	// `type`, and at subs_per_page=50 -- and every result stated its type, so
	// there is no measured case of an untyped result at all, let alone one
	// preceding a typed one. `type` is also server-validated (an empty, unknown
	// or comma-joined value is rejected with HTTP 422), so the single result
	// can only be the kind asked for. The preference below is ordering
	// independence for a shape the catalogue may yet produce, not a fix for a
	// reproduced wrong match.
	sdID := 0
	found := false
	untypedID := 0
	haveUntyped := false
	for _, r := range searchResp.Results {
		if r.Type == wantType {
			sdID, found = r.SDId, true
			break
		}
		if r.Type == "" && !haveUntyped {
			untypedID, haveUntyped = r.SDId, true
		}
	}
	if !found && haveUntyped {
		sdID, found = untypedID, true
	}
	if !found {
		return nil, fmt.Errorf("no %s results for %q", wantType, title)
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
		// A full-season pack is kept, not skipped. It is a zip holding one file
		// per episode, and DownloadAndExtract takes the requested episode out
		// of it; dropping packs here instead threw away results the download
		// path can already use. Measured 2026-10-22: every English subtitle
		// SubDL has for Breaking Bad is a full-season pack, so an episode
		// request answered with ten packs returned nothing at all.
		//
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

// preferSRT keeps whichever of two zip entries is the better subtitle to
// extract: an .srt over any other extension, and otherwise the one already held.
func preferSRT(cur, f *zip.File) *zip.File {
	if cur == nil {
		return f
	}
	if strings.EqualFold(filepath.Ext(f.Name), ".srt") && !strings.EqualFold(filepath.Ext(cur.Name), ".srt") {
		return f
	}
	return cur
}

// isMacOSZipArtifact reports whether a zip entry is macOS packaging debris
// rather than content. Zipping on macOS adds an AppleDouble resource fork per
// file under __MACOSX/, named ._<original>, so it carries the real subtitle's
// extension and looks like a second subtitle to anything counting candidates.
func isMacOSZipArtifact(name string) bool {
	// Zip entries use forward slashes, but some writers emit backslashes.
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.EqualFold(seg, "__MACOSX") || strings.HasPrefix(seg, "._") {
			return true
		}
	}
	return false
}

// episodeMarkerText picks the part of a zip entry's path that decides which
// episode the entry holds. The file's own name wins whenever it states one: a
// pack laid out as show.s01e01.s01e02/s01e01.srt has a directory naming both
// episodes it covers, and letting that speak for each file makes every file in
// it answer for every episode. Only when the basename states nothing does the
// surrounding path get a say, which is what a single release unpacked into a
// release-named folder has to go on.
func episodeMarkerText(name string, season int) string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if len(episodeNumbersIn(base, season)) > 0 {
		return base
	}
	return name
}

// fetchAPI makes a GET request to the SubDL API and returns the parsed response.
func (s *SubDLClient) fetchAPI(params url.Values) (*subdlResponse, error) {
	reqURL := fmt.Sprintf("%s?%s", subdlAPI, params.Encode())

	// The api_key is a query parameter, so every *url.Error on this path
	// stringifies it. fetchAPI's error reaches debugf, which --debug prints
	// and users paste into bug reports, so the URL error is unwrapped to its
	// cause on both the construction and the transport branch.
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("SubDL request: %w", httputil.CauseWithoutURL(err))
	}
	req.Header.Set("User-Agent", "lobster/0.6")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SubDL request: %w", httputil.CauseWithoutURL(err))
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
// Season/episode are used to filter out wrong-episode files from multi-episode zips.
func (s *SubDLClient) DownloadAndExtract(zipURL string, tmpDir *TempDir, season, episode int) (string, error) {
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

	// Find the subtitle file to extract, preferring one that names the episode.
	//
	// The wrong-episode filter only rejects a *stated* mismatch, so an entry
	// stating no episode passes it -- which in a season pack means the first
	// stray file wins over the episode actually asked for. So a named match is
	// collected separately from an unnamed candidate.
	var named, unnamed *zip.File
	otherEpisodes := false
	// Entries differing only by extension are one release in several formats,
	// which is what preferSRT is for, so they are counted once. Entries with
	// genuinely different names are rival candidates.
	unnamedReleases := map[string]bool{}
	for _, f := range reader.File {
		// Resource forks keep the real file's extension, so they have to go
		// before the extension filter rather than after the counting.
		if isMacOSZipArtifact(f.Name) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		switch ext {
		case ".srt", ".vtt", ".ass":
		default:
			continue
		}
		if episode > 0 {
			name := strings.ToLower(f.Name)
			marker := episodeMarkerText(name, season)
			// Skip files clearly for a different episode
			if isWrongEpisode(marker, season, episode) {
				otherEpisodes = true
				continue
			}
			if namesEpisode(marker, season, episode) {
				named = preferSRT(named, f)
				continue
			}
			unnamedReleases[strings.TrimSuffix(name, ext)] = true
		}
		unnamed = preferSRT(unnamed, f)
	}

	best := named
	if best == nil {
		// An archive naming other episodes is a pack, and if none of them is
		// the one asked for then it does not hold this episode -- extracting
		// some unnamed entry out of it is how the wrong episode's subtitles get
		// played. A single release's zip names no episode at all, and its file
		// is the right one, so the fallback is kept for that shape only.
		//
		// Several candidates stating no episode is the same problem wearing a
		// different hat: a pack of 01.srt, 02.srt, 03.srt states nothing this
		// recognises, so nothing distinguishes the asked-for episode from its
		// neighbours and whichever entry the loop happened to keep would be
		// served as episode 3. Refuse rather than guess; the fallback only
		// answers when it is the one candidate in the archive.
		if otherEpisodes || len(unnamedReleases) > 1 {
			return "", fmt.Errorf("no subtitle for season %d episode %d in zip", season, episode)
		}
		best = unnamed
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
