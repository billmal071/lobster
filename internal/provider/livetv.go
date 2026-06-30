package provider

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const liveTVUA = "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0"

// LiveTV streams free public IPTV (iptv-org) plus user playlists. Channels are
// loaded lazily and cached for the session. It implements StreamProvider; the
// TUI also calls Categories/Channels for two-level browsing.
type LiveTV struct {
	client   httpDoer
	sources  []string
	channels []Channel
	byID     map[string]Channel
	byCat    map[string][]Channel
	cats     []string
	once     sync.Once
	loadErr  error
}

func NewLiveTV(sources []string) *LiveTV {
	return &LiveTV{client: httputil.NewClient(), sources: sources}
}

// fetch reads a source: http(s) via the client, anything else via os.ReadFile.
func (p *LiveTV) fetch(src string) ([]byte, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, err := http.NewRequest(http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", liveTVUA)
		resp, err := p.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("livetv: status %d for %s", resp.StatusCode, src)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	}
	return os.ReadFile(src)
}

// load fetches+parses+merges all sources once. A failed source is skipped; only
// if every source fails is loadErr set.
func (p *LiveTV) load() { p.once.Do(p.doLoad) }

func (p *LiveTV) doLoad() {
	p.byID = map[string]Channel{}
	p.byCat = map[string][]Channel{}
	var anyOK bool
	for _, src := range p.sources {
		data, err := p.fetch(src)
		if err != nil {
			continue
		}
		anyOK = true
		for _, ch := range ParseM3U(data) {
			if ch.URL == "" {
				continue
			}
			ch.ID = p.uniqueID(ch.ID)
			p.byID[ch.ID] = ch
			p.channels = append(p.channels, ch)
			for _, cat := range ch.Categories {
				p.byCat[cat] = append(p.byCat[cat], ch)
			}
		}
	}
	if !anyOK && len(p.sources) > 0 {
		p.loadErr = fmt.Errorf("livetv: all %d source(s) failed", len(p.sources))
		return
	}
	for c := range p.byCat {
		p.cats = append(p.cats, c)
	}
	sort.Slice(p.cats, func(i, j int) bool { return liveCatLess(p.cats[i], p.cats[j]) })
}

func (p *LiveTV) uniqueID(base string) string {
	if base == "" {
		base = "channel"
	}
	id := base
	for n := 2; ; n++ {
		if _, exists := p.byID[id]; !exists {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

var liveCatPriority = map[string]int{"sports": 0, "news": 1, "movies": 2}

func liveCatLess(a, b string) bool {
	pa, oka := liveCatPriority[strings.ToLower(a)]
	pb, okb := liveCatPriority[strings.ToLower(b)]
	if oka && okb {
		return pa < pb
	}
	if oka != okb {
		return oka
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

func channelResult(ch Channel) media.SearchResult {
	return media.SearchResult{ID: ch.ID, Title: ch.Name, Type: media.Movie, Poster: ch.Logo}
}

// Categories returns one result per category, with the channel count in Episodes.
func (p *LiveTV) Categories() ([]media.SearchResult, error) {
	p.load()
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	out := make([]media.SearchResult, 0, len(p.cats))
	for _, c := range p.cats {
		out = append(out, media.SearchResult{ID: c, Title: c, Episodes: len(p.byCat[c])})
	}
	return out, nil
}

// Channels returns the channels in a category as Movie-typed results.
func (p *LiveTV) Channels(category string) ([]media.SearchResult, error) {
	p.load()
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	chans := p.byCat[category]
	out := make([]media.SearchResult, 0, len(chans))
	for _, ch := range chans {
		out = append(out, channelResult(ch))
	}
	return out, nil
}

// --- StreamProvider surface ---

func (p *LiveTV) Search(query string) ([]media.SearchResult, error) {
	p.load()
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []media.SearchResult
	for _, ch := range p.channels {
		if q == "" || strings.Contains(strings.ToLower(ch.Name), q) {
			out = append(out, channelResult(ch))
		}
	}
	return out, nil
}

func (p *LiveTV) Watch(mediaID, _, _, _ string) (*media.Stream, error) {
	p.load()
	if p.loadErr != nil {
		return nil, p.loadErr
	}
	ch, ok := p.byID[mediaID]
	if !ok {
		return nil, fmt.Errorf("livetv: unknown channel %q", mediaID)
	}
	return &media.Stream{URL: ch.URL, Referer: ch.Referer, UserAgent: ch.UserAgent}, nil
}

func (p *LiveTV) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (p *LiveTV) GetSeasons(id string) ([]media.Season, error) {
	return []media.Season{{Number: 1, ID: id}}, nil
}
func (p *LiveTV) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return []media.Episode{{Number: 1, ID: id}}, nil
}
func (p *LiveTV) GetServers(id, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "LiveTV", ID: "default"}}, nil
}
func (p *LiveTV) GetEmbedURL(serverID string) (string, error)               { return "", fmt.Errorf("use Watch") }
func (p *LiveTV) Trending(mt media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (p *LiveTV) Recent(mt media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
