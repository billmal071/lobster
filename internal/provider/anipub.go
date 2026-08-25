package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const (
	anipubBase   = "https://anipub.xyz"
	megaplayBase = "https://megaplay.buzz"
	anipubUA     = "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0"
)

// AniPub streams FINISHED anime via anipub.xyz -> megaplay.buzz. All open JSON,
// no anti-bot/decrypt. Used as a fallback behind AllAnime (which can't reliably
// stream finished series).
type AniPub struct {
	client httpDoer
}

func NewAniPub() *AniPub { return &AniPub{client: httputil.NewClient()} }

func (p *AniPub) get(rawURL string, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 700 * time.Millisecond) // back off 429s
		}
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", anipubUA)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("anipub: status 429")
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("anipub: status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		return data, err
	}
	return nil, lastErr
}

func (p *AniPub) Search(query string) ([]media.SearchResult, error) {
	body, err := p.get(anipubBase+"/api/search/"+url.PathEscape(query), nil)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	type anipubHit struct {
		Name  string `json:"Name"`
		ID    int    `json:"Id"`
		Image string `json:"Image"`
	}
	var raw []anipubHit
	t := bytes.TrimSpace(body)
	switch {
	case len(t) > 0 && t[0] == '[':
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("search parse: %w", err)
		}
	case len(t) > 0 && t[0] == '{':
		// A single match comes back as a bare object; anything else
		// object-shaped (e.g. {"found":false}) means no results.
		var one anipubHit
		if err := json.Unmarshal(body, &one); err == nil && one.ID != 0 && one.Name != "" {
			raw = append(raw, one)
		}
	}
	out := make([]media.SearchResult, 0, len(raw))
	for _, r := range raw {
		out = append(out, media.SearchResult{
			ID: strconv.Itoa(r.ID), Title: r.Name, Type: media.TV, Poster: r.Image,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no anime found for %q", query)
	}
	return out, nil
}

// Episode links carry the stream reference in one of three shapes:
//
//	...gogoanime.com.by/streaming.php?...&ep=1465&...   (megaplay id 1465)
//	...anipub.xyz/video/850/sub                          (megaplay id 850)
//	...anipub.xyz/play/63468/2/sub                       (MAL id 63468, ep 2)
var (
	anipubEpRe   = regexp.MustCompile(`(?:ep=|/video/)(\d+)`)
	anipubPlayRe = regexp.MustCompile(`/play/(\d+)/(\d+)/`)
)

// episodeMegaplayIDs returns the ordered per-episode stream refs for a show
// id: either a bare megaplay id (resolved via /stream/s-2/) or "mal:{id}:{ep}"
// (resolved via /stream/mal/). In the /play/ shape the top-level link is
// episode 1 and the ep array continues from episode 2, so both are read.
func (p *AniPub) episodeMegaplayIDs(showID string) ([]string, error) {
	body, err := p.get(anipubBase+"/v1/api/details/"+showID, nil)
	if err != nil {
		return nil, err
	}
	var d struct {
		Local struct {
			Link string `json:"link"`
			Ep   []struct {
				Link string `json:"link"`
			} `json:"ep"`
		} `json:"local"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, err
	}
	links := make([]string, 0, len(d.Local.Ep)+1)
	if d.Local.Link != "" {
		links = append(links, d.Local.Link)
	}
	for _, e := range d.Local.Ep {
		links = append(links, e.Link)
	}
	ids := make([]string, 0, len(links))
	seen := make(map[string]bool, len(links))
	for _, l := range links {
		var id string
		if m := anipubPlayRe.FindStringSubmatch(l); m != nil {
			id = "mal:" + m[1] + ":" + m[2]
		} else if m := anipubEpRe.FindStringSubmatch(l); m != nil {
			id = m[1]
		} else {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

var megaplayDataIDRe = regexp.MustCompile(`data-id="(\d+)"`)

// megaplayStream resolves a megaplay episode id + audio to a playable stream.
func (p *AniPub) megaplayStream(megaplayID string, dub bool) (*media.Stream, error) {
	audio := "sub"
	if dub {
		audio = "dub"
	}
	streamURL := fmt.Sprintf("%s/stream/s-2/%s/%s", megaplayBase, megaplayID, audio)
	if rest, ok := strings.CutPrefix(megaplayID, "mal:"); ok {
		if malID, epNum, ok := strings.Cut(rest, ":"); ok {
			streamURL = fmt.Sprintf("%s/stream/mal/%s/%s/%s", megaplayBase, malID, epNum, audio)
		}
	}
	page, err := p.get(streamURL,
		map[string]string{"Referer": "https://gogoanime.com.by/"})
	if err != nil {
		return nil, err
	}
	m := megaplayDataIDRe.FindSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("megaplay: no data-id")
	}
	body, err := p.get(fmt.Sprintf("%s/stream/getSources?id=%s", megaplayBase, m[1]),
		map[string]string{"Referer": "https://megaplay.buzz/", "X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return nil, err
	}
	var src struct {
		Sources struct {
			File string `json:"file"`
		} `json:"sources"`
		Tracks []struct {
			File  string `json:"file"`
			Label string `json:"label"`
			Kind  string `json:"kind"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(body, &src); err != nil {
		return nil, err
	}
	if src.Sources.File == "" {
		return nil, fmt.Errorf("megaplay: no source file")
	}
	st := &media.Stream{URL: src.Sources.File, Referer: "https://megaplay.buzz/", Deobfuscate: true}
	for _, tr := range src.Tracks {
		if tr.Kind != "captions" {
			continue
		}
		st.Subtitles = append(st.Subtitles, media.Subtitle{Language: tr.Label, Label: tr.Label, URL: tr.File})
	}
	return st, nil
}

// ResolveByTitle is the fallback entry point: find the show by title, take
// episode episodeNum (1-based), and resolve its stream.
func (p *AniPub) ResolveByTitle(title string, episodeNum int, dub bool) (*media.Stream, error) {
	res, err := p.searchVariants(title)
	if err != nil {
		return nil, err
	}
	ids, err := p.episodeMegaplayIDs(bestAniPubMatch(res, title))
	if err != nil {
		return nil, err
	}
	if episodeNum < 1 || episodeNum > len(ids) {
		return nil, fmt.Errorf("anipub: episode %d out of range (have %d)", episodeNum, len(ids))
	}
	return p.megaplayStream(ids[episodeNum-1], dub)
}

// searchVariants tries the full title then a subtitle-stripped form, so AllAnime
// titles carrying a subtitle AniPub doesn't index (e.g. "Code Geass: ... R2")
// still resolve.
func (p *AniPub) searchVariants(title string) ([]media.SearchResult, error) {
	var lastErr error
	for _, v := range titleVariants(title) {
		if res, err := p.Search(v); err == nil && len(res) > 0 {
			return res, nil
		} else if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no anime found for %q", title)
	}
	return nil, lastErr
}

func titleVariants(title string) []string {
	t := strings.TrimSpace(title)
	out := []string{t}
	if i := strings.LastIndex(t, ":"); i > 0 {
		if base := strings.TrimSpace(t[:i]); base != t {
			out = append(out, base)
		}
	}
	return out
}

// bestAniPubMatch picks the result whose name shares the most words with the
// full title (exact match wins), so a broadened search still lands on the
// right entry rather than an unrelated first result.
func bestAniPubMatch(res []media.SearchResult, title string) string {
	for _, r := range res {
		if strings.EqualFold(strings.TrimSpace(r.Title), strings.TrimSpace(title)) {
			return r.ID
		}
	}
	want := titleWords(title)
	bestID, best := res[0].ID, -1<<30
	for _, r := range res {
		rw := titleWords(r.Title)
		ov := wordOverlap(rw, want)
		// Maximize shared words, then penalize EXTRA words so the clean main
		// entry beats a same-overlap spinoff ("... OVA Collection", "... Specials").
		score := ov*1000 - (len(rw) - ov)
		if score > best {
			best, bestID = score, r.ID
		}
	}
	return bestID
}

func titleWords(s string) map[string]bool {
	w := map[string]bool{}
	for _, f := range strings.Fields(strings.ToLower(s)) {
		if f = strings.Trim(f, ":;,.-"); f != "" {
			w[f] = true
		}
	}
	return w
}

func wordOverlap(a, b map[string]bool) int {
	n := 0
	for k := range a {
		if b[k] {
			n++
		}
	}
	return n
}

// --- StreamProvider surface (usable standalone too) ---

func (p *AniPub) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}

func (p *AniPub) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	ids, err := p.episodeMegaplayIDs(id)
	if err != nil {
		return nil, fmt.Errorf("episodes: %w", err)
	}
	out := make([]media.Episode, 0, len(ids))
	for i, mid := range ids {
		out = append(out, media.Episode{Number: i + 1, Title: fmt.Sprintf("Episode %d", i+1), ID: mid})
	}
	return out, nil
}

func (p *AniPub) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	// Native refs are bare megaplay ids ("1466") or "mal:{id}:{ep}"; anything
	// else with colons (or empty) is the fallback-resolver numeric form.
	if episodeID == "" || (!strings.HasPrefix(episodeID, "mal:") && strings.Contains(episodeID, ":")) {
		nid, err := resolveNumericEpisodeID(p.GetEpisodes, mediaID, episodeID)
		if err != nil {
			return nil, err
		}
		episodeID = nid
	}
	return p.megaplayStream(episodeID, strings.EqualFold(server, "dub"))
}

func (p *AniPub) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (p *AniPub) GetServers(id, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AniPub", ID: "default"}}, nil
}
func (p *AniPub) GetEmbedURL(serverID string) (string, error)               { return "", fmt.Errorf("use Watch") }
func (p *AniPub) Trending(mt media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (p *AniPub) Recent(mt media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
