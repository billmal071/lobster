package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"lobster/internal/media"
)

const liveTVUA = "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0"

// LiveTV streams free public IPTV (iptv-org) plus user playlists. Channels are
// loaded lazily and cached for the session. It implements StreamProvider; the
// TUI also calls Categories/Channels for two-level browsing.
type LiveTV struct {
	client   httpDoer // primary fetch client (TLS 1.3 capable)
	fallback httpDoer // retried when the primary errors (TLS 1.2-capped; nil in tests)
	sources  []string
	channels []Channel
	byID     map[string]Channel
	byCat    map[string][]Channel
	cats     []string
	once     sync.Once
	loadErr  error
	failed   []string
}

func NewLiveTV(sources []string) *LiveTV {
	return &LiveTV{
		client:   liveTVHTTPClient(tls.VersionTLS13),
		fallback: liveTVHTTPClient(tls.VersionTLS12),
		sources:  sources,
	}
}

// liveTVHTTPClient builds a playlist-fetch client. A short TLSHandshakeTimeout
// makes a stalled handshake fail fast instead of hanging on the overall timeout,
// and maxVer lets the caller cap the TLS version: some networks (e.g. certain
// phone hotspots / middleboxes) break TLS 1.3 handshakes to CDNs like GitHub
// Pages, so we retry capped at TLS 1.2.
func liveTVHTTPClient(maxVer uint16) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second, // a slow CDN serving a ~3 MB playlist
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 12 * time.Second,
			ForceAttemptHTTP2:   true,
			IdleConnTimeout:     30 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: maxVer},
		},
	}
}

// fetch reads a source: http(s) via the client, anything else via os.ReadFile.
// On an http error it retries once with the TLS 1.2-capped fallback client.
func (p *LiveTV) fetch(ctx context.Context, src string) ([]byte, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		data, err := p.httpGet(ctx, p.client, src)
		// The TLS 1.2 retry re-runs the whole GET, so it is attempted only
		// when the budget still allows it. Without this check a single
		// unreachable source costs two full client timeouts.
		if err != nil && p.fallback != nil && ctx.Err() == nil {
			if data2, err2 := p.httpGet(ctx, p.fallback, src); err2 == nil {
				return data2, nil
			}
		}
		return data, err
	}
	return readFileContext(ctx, src)
}

// readFileContext reads a local playlist under ctx.
//
// os.ReadFile alone cannot be cancelled, and a local source is not always
// fast: a path on a stalled network mount, or a FIFO nothing ever writes to,
// blocks the read forever. LoadContext waits on every source, so one such
// path would keep the whole agent-facing command blocked long past
// LiveLoadBudget — the deadline the command advertises. The read therefore
// runs on its own goroutine and ctx wins the race.
//
// The path is stat'd first and anything that is not a regular file is
// refused before os.Open is called. This is not redundant with the ctx race
// below: ctx frees the *caller*, but nothing can interrupt a read syscall
// already blocked in the kernel, so an abandoned goroutine on a FIFO lives
// as long as the process does. A `channels` run exits moments later and the
// leak is invisible; a `play --ref` that resolves through another source
// keeps running for the length of the broadcast, holding that goroutine and
// its open descriptor the whole time. os.Stat does not block on a FIFO the
// way os.Open does, so refusing here costs nothing and removes the only case
// that can strand a goroutine indefinitely. Directories and device files are
// refused by the same check; none of them is a playlist.
//
// The ctx race still earns its place for what stat cannot see: a regular
// file on a stalled network mount blocks in the read itself, after a stat
// that looked perfectly ordinary. The goroutine is not cancelled by
// returning, so it must not be allowed to block on delivery either: the
// channel is buffered, so the abandoned read's result is discarded when it
// eventually completes and the goroutine exits rather than leaking for the
// life of the process.
//
// The same maxPlaylistBytes cap as httpGet applies. A local playlist is
// bounded by an io.LimitReader rather than trusted for its stat size: a
// FIFO or /dev/zero reports no meaningful size, and os.ReadFile on either
// would grow its buffer until the process died.
func readFileContext(ctx context.Context, path string) ([]byte, error) {
	type readResult struct {
		data []byte
		err  error
	}
	// An already-expired ctx returns before any filesystem work. doLoadContext
	// checks this too, so this is belt-and-braces there — but it makes the
	// cancellation contract hold for any caller and, unlike the select below
	// (where two ready cases are chosen between at random), it is
	// deterministic enough to test.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("livetv: reading %s: %w", path, err)
	}

	ch := make(chan readResult, 1)
	go func() {
		// Inside the goroutine, and before os.Open. Inside, because os.Stat
		// is itself a blocking syscall: on a stalled network mount it hangs
		// exactly like the read does, so hoisting it above the select traded
		// one unbounded call for another and put it back on the path that
		// must stay cancellable. Before the open, because that is what makes
		// the FIFO refusal work at all — stat returns promptly on a FIFO,
		// where open blocks until a writer arrives.
		st, err := os.Stat(path)
		if err != nil {
			ch <- readResult{err: err}
			return
		}
		if !st.Mode().IsRegular() {
			ch <- readResult{err: fmt.Errorf("livetv: playlist %s is not a regular file", path)}
			return
		}
		f, err := os.Open(path)
		if err != nil {
			ch <- readResult{err: err}
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, maxPlaylistBytes+1))
		if err != nil {
			ch <- readResult{err: err}
			return
		}
		if len(data) > maxPlaylistBytes {
			ch <- readResult{err: fmt.Errorf("livetv: playlist %s exceeds %d MiB limit", path, maxPlaylistBytes>>20)}
			return
		}
		ch <- readResult{data: data}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("livetv: reading %s: %w", path, ctx.Err())
	case r := <-ch:
		return r.data, r.err
	}
}

// maxPlaylistBytes caps a single playlist download.
const maxPlaylistBytes = 32 << 20

func (p *LiveTV) httpGet(ctx context.Context, c httpDoer, src string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		// Redacted like every other error here. A malformed source string
		// reaches this branch carrying whatever the user configured — Xtream
		// credentials included — and doLoadContext folds the last error into
		// loadErr, which the agent commands print verbatim as JSON.
		return nil, fmt.Errorf("livetv: building request for %s failed: %w", redactURL(src), err)
	}
	req.Header.Set("User-Agent", liveTVUA)
	resp, err := c.Do(req)
	if err != nil {
		// A *url.Error embeds the full URL (which may carry Xtream
		// username/password); return the redacted URL plus the underlying cause.
		cause := err
		var ue *url.Error
		if errors.As(err, &ue) {
			cause = ue.Err
		}
		return nil, fmt.Errorf("livetv: fetch %s failed: %w", redactURL(src), cause)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("livetv: status %d for %s", resp.StatusCode, redactURL(src))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPlaylistBytes {
		return nil, fmt.Errorf("livetv: playlist %s exceeds %d MiB limit", redactURL(src), maxPlaylistBytes>>20)
	}
	return data, nil
}

// redactURL strips the query string and userinfo so credentials (e.g. Xtream
// username/password) never appear in error messages or logs.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}

// LiveLoadBudget bounds a whole playlist load for the agent-facing commands.
// It is deliberately larger than find's 5s race: a single playlist can be
// several MB. One constant, not one per call site.
const LiveLoadBudget = 15 * time.Second

// liveLoadConcurrency caps simultaneous playlist fetches.
const liveLoadConcurrency = 4

// load fetches+parses+merges all sources once, without a deadline. Retained
// for the TUI, which is not an agent-facing path.
func (p *LiveTV) load() { _ = p.LoadContext(context.Background()) }

// LoadContext fetches every source once, in parallel and bounded by ctx, and
// merges the results in declared source order. A failed source is recorded in
// FailedSources and skipped; only if every source fails is an error returned.
// Zero sources is success with nothing, not an error — callers distinguish
// "nothing configured" themselves.
func (p *LiveTV) LoadContext(ctx context.Context) error {
	p.once.Do(func() { p.doLoadContext(ctx) })
	return p.loadErr
}

func (p *LiveTV) doLoadContext(ctx context.Context) {
	p.byID = map[string]Channel{}
	p.byCat = map[string][]Channel{}

	// Indexed by source position so the merge below is independent of
	// completion order.
	type result struct {
		data []byte
		err  error
	}
	results := make([]result, len(p.sources))

	// A fixed worker pool, not a goroutine per source with a semaphore.
	// The source list is not bounded by anything the user typed: liveTVSources
	// merges the configured playlists with every live playlist the remote
	// TBCPL catalog names, so its length is remote input. Launching one
	// goroutine per source would allocate stacks for all of them up front,
	// and the semaphore inside would bound only how many are *fetching* — not
	// how many exist. The pool caps both, and does so before any work starts.
	workers := liveLoadConcurrency
	if len(p.sources) < workers {
		workers = len(p.sources)
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if ctx.Err() != nil {
					results[i] = result{err: ctx.Err()}
					continue
				}
				data, err := p.fetch(ctx, p.sources[i])
				results[i] = result{data: data, err: err}
			}
		}()
	}
	for i := range p.sources {
		idx <- i
	}
	close(idx)
	wg.Wait()

	var anyOK bool
	var lastErr error
	for i, src := range p.sources {
		if results[i].err != nil {
			lastErr = results[i].err
			p.failed = append(p.failed, src)
			continue
		}
		anyOK = true
		for _, ch := range ParseM3U(results[i].data) {
			if ch.URL == "" {
				continue
			}
			ch.Source = src
			ch.ID = p.uniqueID(ch.ID)
			p.byID[ch.ID] = ch
			p.channels = append(p.channels, ch)
			for _, cat := range ch.Categories {
				p.byCat[cat] = append(p.byCat[cat], ch)
			}
		}
	}
	if !anyOK && len(p.sources) > 0 {
		p.loadErr = fmt.Errorf("livetv: all %d source(s) failed: %w", len(p.sources), lastErr)
		return
	}
	for c := range p.byCat {
		p.cats = append(p.cats, c)
	}
	sort.Slice(p.cats, func(i, j int) bool { return liveCatLess(p.cats[i], p.cats[j]) })
}

// FailedSources returns the sources that did not load, in declared order.
// It is what lets a caller tell "this channel is gone" from "the playlist it
// lives in did not load" — two conditions that need different exit codes.
// The returned slice is a copy: a caller that sorted or rewrote it in place
// would otherwise be editing provider state, and this one is read again by
// resolveLiveRef after the caller has already reported it.
func (p *LiveTV) FailedSources() []string { return append([]string(nil), p.failed...) }

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

// ChannelKey identifies a channel across reloads. TVGID is checked first when
// present, and Lookup itself matches on TVGID alone in that case — it does
// not also consult Name. Name is the fallback identifier for playlists that
// omit tvg-id, and matched only when TVGID is empty. Source narrows either
// case to one playlist.
//
// A TVGID match is not guaranteed unique: real playlists are not always
// disciplined about tvg-id uniqueness, so more than one channel can share
// one. Lookup returns every such match rather than choosing one — see its
// own doc comment. A caller that needs a single channel out of a shared
// tvg-id (cmd.resolveLiveRef) narrows the result further by exact folded
// Title itself; that narrowing happens above Lookup, not inside it.
type ChannelKey struct {
	TVGID  string
	Name   string
	Source string
}

// cloneChannel returns a copy of ch that shares no backing array with it.
// A plain struct copy duplicates Categories' slice header, not its backing
// array, so callers could otherwise mutate provider state through it.
func cloneChannel(ch Channel) Channel {
	ch.Categories = append([]string(nil), ch.Categories...)
	return ch
}

// AllChannels returns every loaded channel in merge order. It does not
// trigger a load; callers load first (LoadContext for the bounded path).
// Each returned Channel is a deep copy (including Categories), so a caller
// cannot mutate provider state through it.
func (p *LiveTV) AllChannels() []Channel {
	out := make([]Channel, len(p.channels))
	for i, ch := range p.channels {
		out[i] = cloneChannel(ch)
	}
	return out
}

// Lookup returns every channel matching k. It returns all matches rather
// than one so the caller can detect ambiguity and refuse: picking the first
// would reintroduce playlist iteration order as the tie-break. It does not
// trigger a load; callers load first. Each returned Channel is a deep copy,
// as with AllChannels.
func (p *LiveTV) Lookup(k ChannelKey) []Channel {
	name := strings.ToLower(strings.TrimSpace(k.Name))
	var out []Channel
	for _, ch := range p.channels {
		if k.Source != "" && ch.Source != k.Source {
			continue
		}
		if k.TVGID != "" {
			if ch.TVGID == k.TVGID {
				out = append(out, cloneChannel(ch))
			}
			continue
		}
		if name != "" && strings.ToLower(strings.TrimSpace(ch.Name)) == name {
			out = append(out, cloneChannel(ch))
		}
	}
	return out
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
