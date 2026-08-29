package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// ytsBase is the YTS public JSON API. A var so tests can point it at a stub.
// yts.mx does not answer from every network; yts.gg serves the same API.
var ytsBase = "https://yts.gg"

// ytsTrackers are the announce URLs YTS itself puts in its magnets. A magnet
// with no trackers has only DHT to work with and frequently never finds a peer,
// so playback appears to hang rather than start.
var ytsTrackers = []string{
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.openbittorrent.com:80",
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://torrent.gresille.org:80/announce",
	"udp://p4p.arenabg.com:1337",
	"udp://tracker.leechers-paradise.org:6969",
	"udp://tracker.coppersurfer.tk:6969",
	"udp://glotorrents.pw:6969/announce",
}

// YTS resolves movies to BitTorrent magnets from the YTS public API.
//
// Unlike every other provider here it returns a magnet rather than an HTTP
// stream: playback goes through the local torrent server, which downloads
// pieces in order and serves them over loopback.
type YTS struct {
	client *http.Client
}

func NewYTS() *YTS { return &YTS{client: httputil.NewClient()} }

type ytsTorrent struct {
	Hash    string `json:"hash"`
	Quality string `json:"quality"`
	Type    string `json:"type"`
	Size    string `json:"size"`
	Seeds   int    `json:"seeds"`
}

type ytsMovie struct {
	ID       int          `json:"id"`
	Title    string       `json:"title"`
	Year     int          `json:"year"`
	Runtime  int          `json:"runtime"`
	Cover    string       `json:"medium_cover_image"`
	Torrents []ytsTorrent `json:"torrents"`
}

// ytsResponse covers both shapes the API uses: list_movies.json returns
// data.movies as a list, movie_details.json returns data.movie as one object.
type ytsResponse struct {
	Data struct {
		Movies []ytsMovie `json:"movies"`
		Movie  *ytsMovie  `json:"movie"`
	} `json:"data"`
}

func (y *YTS) fetch(apiURL string) (*ytsResponse, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := y.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yts request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yts: unexpected status %d", resp.StatusCode)
	}
	var out ytsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("parsing yts response: %w", err)
	}
	return &out, nil
}

// Search queries the YTS catalogue. It is movies only — YTS has no TV.
func (y *YTS) Search(query string) ([]media.SearchResult, error) {
	apiURL := fmt.Sprintf("%s/api/v2/list_movies.json?limit=20&query_term=%s",
		strings.TrimRight(ytsBase, "/"), url.QueryEscape(query))
	resp, err := y.fetch(apiURL)
	if err != nil {
		return nil, err
	}
	results := make([]media.SearchResult, 0, len(resp.Data.Movies))
	for _, m := range resp.Data.Movies {
		if len(m.Torrents) == 0 {
			continue
		}
		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("yts/%d", m.ID),
			Title:  m.Title,
			Year:   strconv.Itoa(m.Year),
			Type:   media.Movie,
			Poster: m.Cover,
		})
	}
	return results, nil
}

// movieByID re-queries a single movie. Watch and GetServers both need the
// torrent list, and the list endpoint is the only one that carries it.
func (y *YTS) movieByID(id string) (*ytsMovie, error) {
	numeric := strings.TrimPrefix(id, "yts/")
	if err := httputil.ValidateNumericID(numeric); err != nil {
		return nil, fmt.Errorf("yts: invalid movie ID %q", id)
	}
	apiURL := fmt.Sprintf("%s/api/v2/movie_details.json?movie_id=%s",
		strings.TrimRight(ytsBase, "/"), numeric)
	resp, err := y.fetch(apiURL)
	if err != nil {
		return nil, err
	}
	if resp.Data.Movie != nil && len(resp.Data.Movie.Torrents) > 0 {
		return resp.Data.Movie, nil
	}
	if len(resp.Data.Movies) > 0 {
		return &resp.Data.Movies[0], nil
	}
	return nil, fmt.Errorf("yts: movie %s not found", id)
}

// sortedTorrents returns the torrents highest-quality first.
func sortedTorrents(m *ytsMovie) []ytsTorrent {
	out := make([]ytsTorrent, len(m.Torrents))
	copy(out, m.Torrents)
	sort.SliceStable(out, func(i, j int) bool {
		return ytsQualityHeight(out[i].Quality) > ytsQualityHeight(out[j].Quality)
	})
	return out
}

// ytsQualityHeight turns "1080p"/"2160p" into a comparable number.
func ytsQualityHeight(q string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(q)), "p"))
	return n
}

// GetServers lists one entry per available torrent. Seed count is part of the
// name because it is the difference between a stream that plays and one that
// stalls, and the user is the one choosing.
func (y *YTS) GetServers(id string, episodeID string) ([]media.Server, error) {
	m, err := y.movieByID(id)
	if err != nil {
		return nil, err
	}
	torrents := sortedTorrents(m)
	servers := make([]media.Server, 0, len(torrents))
	for i, tr := range torrents {
		servers = append(servers, media.Server{
			Name: fmt.Sprintf("%s %s (%s, %d seeds)", tr.Quality, tr.Type, tr.Size, tr.Seeds),
			ID:   strconv.Itoa(i),
		})
	}
	return servers, nil
}

// pickTorrent chooses by explicit server name first, then requested quality,
// then the highest available. YTS does not stock every rung for every film, so
// an unavailable quality falls back rather than failing.
func pickTorrent(torrents []ytsTorrent, server, quality string) ytsTorrent {
	for i, tr := range torrents {
		name := fmt.Sprintf("%s %s (%s, %d seeds)", tr.Quality, tr.Type, tr.Size, tr.Seeds)
		if server != "" && (strings.EqualFold(server, name) || server == strconv.Itoa(i)) {
			return tr
		}
	}
	if want := strings.ToLower(strings.TrimSpace(quality)); want != "" && want != "best" {
		if h, err := strconv.Atoi(strings.TrimSuffix(want, "p")); err == nil {
			for _, tr := range torrents {
				if ytsQualityHeight(tr.Quality) == h {
					return tr
				}
			}
		}
	}
	// Highest available: sortedTorrents already ordered them.
	return torrents[0]
}

// magnetFor builds a magnet URI with YTS's own trackers attached.
func magnetFor(hash, title string) string {
	var b strings.Builder
	b.WriteString("magnet:?xt=urn:btih:")
	b.WriteString(hash)
	b.WriteString("&dn=")
	b.WriteString(url.QueryEscape(title))
	for _, tr := range ytsTrackers {
		b.WriteString("&tr=")
		b.WriteString(url.QueryEscape(tr))
	}
	return b.String()
}

// Watch returns a magnet URI. The caller hands it to the torrent server, which
// turns it into a loopback HTTP URL a player can open.
func (y *YTS) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	m, err := y.movieByID(mediaID)
	if err != nil {
		return nil, err
	}
	torrents := sortedTorrents(m)
	if len(torrents) == 0 {
		return nil, fmt.Errorf("yts: no torrents for %s", mediaID)
	}
	tr := pickTorrent(torrents, server, quality)
	return &media.Stream{
		URL:     magnetFor(tr.Hash, fmt.Sprintf("%s (%d)", m.Title, m.Year)),
		Quality: tr.Quality,
	}, nil
}

func (y *YTS) GetDetails(id string) (*media.ContentDetail, error) {
	m, err := y.movieByID(id)
	if err != nil {
		return nil, err
	}
	return &media.ContentDetail{
		Released: strconv.Itoa(m.Year),
		Duration: fmt.Sprintf("%d min", m.Runtime),
	}, nil
}

// YTS is a movie catalogue: it has no seasons, episodes or embeds.
func (y *YTS) GetSeasons(id string) ([]media.Season, error) { return nil, nil }
func (y *YTS) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return nil, nil
}
func (y *YTS) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("yts: use Watch instead")
}

func (y *YTS) Trending(mediaType media.MediaType) ([]media.SearchResult, error) {
	return y.browse("download_count")
}

func (y *YTS) Recent(mediaType media.MediaType) ([]media.SearchResult, error) {
	return y.browse("date_added")
}

func (y *YTS) browse(sortBy string) ([]media.SearchResult, error) {
	apiURL := fmt.Sprintf("%s/api/v2/list_movies.json?limit=20&sort_by=%s",
		strings.TrimRight(ytsBase, "/"), url.QueryEscape(sortBy))
	resp, err := y.fetch(apiURL)
	if err != nil {
		return nil, err
	}
	results := make([]media.SearchResult, 0, len(resp.Data.Movies))
	for _, m := range resp.Data.Movies {
		if len(m.Torrents) == 0 {
			continue
		}
		results = append(results, media.SearchResult{
			ID:     fmt.Sprintf("yts/%d", m.ID),
			Title:  m.Title,
			Year:   strconv.Itoa(m.Year),
			Type:   media.Movie,
			Poster: m.Cover,
		})
	}
	return results, nil
}
