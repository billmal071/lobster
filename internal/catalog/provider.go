package catalog

import (
	"errors"
	"fmt"
	"sync"

	"lobster/internal/media"
	"lobster/internal/provider"
	"lobster/internal/titlekey"
)

// Hint is shown once when no key is configured.
const Hint = "tip: set tmdb_api_key (or LOBSTER_TMDB_KEY) for richer trending and recent lists"

// Kind selects which catalogue lists a Provider serves.
type Kind int

const (
	General Kind = iota
	Anime
)

// Provider serves list and detail rows from the catalogue and delegates
// everything else, playback included, to the wrapped provider. Callers must
// Unwrap before handing it to the playback chain, which type-switches on the
// concrete provider.
type Provider struct {
	provider.Provider
	client *Client
	kind   Kind

	mu       sync.Mutex
	notice   string
	hintDone bool
	rows     map[string]media.SearchResult
	resolved map[resolveKey]string
}

// Wrap returns inner with catalogue lists. A nil client keeps inner's lists
// and queues Hint once on the General kind.
func Wrap(inner provider.Provider, c *Client, k Kind) *Provider {
	return &Provider{Provider: inner, client: c, kind: k}
}

// Unwrap returns the wrapped provider.
func (p *Provider) Unwrap() provider.Provider { return p.Provider }

// TakeNotice returns the pending one-line notice and clears it.
func (p *Provider) TakeNotice() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.notice
	p.notice = ""
	return n
}

func (p *Provider) setNotice(n string) {
	p.mu.Lock()
	p.notice = n
	p.mu.Unlock()
}

func (p *Provider) hintOnce() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kind == General && !p.hintDone {
		p.hintDone = true
		return Hint
	}
	return ""
}

func fallbackNotice(err error) string {
	const tail = "; showing the provider's list instead"
	switch {
	case errors.Is(err, ErrKeyRejected):
		return ErrKeyRejected.Error() + tail
	case errors.Is(err, ErrRateLimited):
		return ErrRateLimited.Error() + tail
	default:
		return fmt.Sprintf("TMDB unavailable (%v)%s", err, tail)
	}
}

func (p *Provider) remember(rows []media.SearchResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rows == nil {
		p.rows = map[string]media.SearchResult{}
	}
	for _, r := range rows {
		if IsID(r.ID) {
			if old, ok := p.rows[r.ID]; ok && r.Year == "" {
				r.Year = old.Year
			}
			p.rows[r.ID] = r
		}
	}
}

func (p *Provider) providerID(id string) (string, error) {
	if !IsID(id) {
		return id, nil
	}
	p.mu.Lock()
	row, known := p.rows[id]
	p.mu.Unlock()
	if !known {
		return id, nil
	}
	if row.Year == "" {
		row = p.Dated(row)
		row.Year = p.keepYear(id, row.Year)
	}
	_, pid, err := p.resolve(row)
	return pid, err
}

// keepYear stores a year recovered for a remembered catalogue ID and returns
// the year to resolve with. A row that gained a year meanwhile, from a newer
// listing, keeps its own, and that year is returned instead.
func (p *Provider) keepYear(id, year string) string {
	if year == "" {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cur, ok := p.rows[id]
	if !ok {
		return year
	}
	if cur.Year == "" {
		cur.Year = year
		p.rows[id] = cur
	}
	return cur.Year
}

// ResolveRow names the wrapped provider's own ID for a catalogue row in
// ProviderID and leaves ID, the catalogue identity, alone. A catalogue row
// the provider has no match for is marked Unresolved, and a later call on
// that row clears it once the provider answers. A row the catalogue did not
// produce is returned unchanged. A yearless row comes back with the catalogue
// year the match was made on.
func (p *Provider) ResolveRow(r media.SearchResult) media.SearchResult {
	if !r.FromCatalogue || !IsID(r.ID) || r.ProviderID != "" {
		return r
	}
	dated, id, err := p.resolve(r)
	if err != nil {
		r.Unresolved = true
		return r
	}
	r = dated
	r.ProviderID = id
	r.Unresolved = false
	return r
}

type resolveKey struct {
	id, title, year string
	mt              media.MediaType
}

func MintsTMDBShapedIDs(p provider.Provider) bool {
	m, ok := p.(interface{ MintsTMDBShapedIDs() bool })
	return ok && m.MintsTMDBShapedIDs()
}

func (p *Provider) resolve(row media.SearchResult) (media.SearchResult, string, error) {
	if MintsTMDBShapedIDs(p.Provider) {
		return row, row.ID, nil
	}
	if row.Year == "" {
		row = p.Dated(row)
		row.Year = p.keepYear(row.ID, row.Year)
	}
	key := resolveKey{row.ID, titlekey.Normalize(row.Title), row.Year, row.Type}
	if key.title == "" {
		return row, "", fmt.Errorf("%q has no title to match on this provider", row.Title)
	}
	if row.Year == "" {
		return row, "", fmt.Errorf("%q has no release year to tell it from a same-titled work on this provider", row.Title)
	}
	p.mu.Lock()
	if r, ok := p.resolved[key]; ok {
		p.mu.Unlock()
		return row, r, nil
	}
	p.mu.Unlock()
	results, err := p.Provider.Search(row.Title)
	if err != nil {
		return row, "", err
	}
	exact := map[string]bool{}
	loose := map[string]bool{}
	for _, r := range results {
		if r.Type != row.Type || titlekey.Normalize(r.Title) != key.title {
			continue
		}
		if row.Year != "" && r.Year != "" && r.Year != row.Year {
			continue
		}
		if row.Year != "" && r.Year == row.Year {
			exact[r.ID] = true
		} else {
			loose[r.ID] = true
		}
	}
	candidates := loose
	if len(exact) > 0 {
		candidates = exact
	}
	if len(candidates) == 0 {
		return row, "", fmt.Errorf("%q was not found on this provider", row.Title)
	}
	if len(candidates) > 1 {
		return row, "", fmt.Errorf("%q matches more than one work on this provider", row.Title)
	}
	pick := ""
	for id := range candidates {
		pick = id
	}
	p.mu.Lock()
	if p.resolved == nil {
		p.resolved = map[resolveKey]string{}
	}
	p.resolved[key] = pick
	p.mu.Unlock()
	return row, pick, nil
}

// Dated fills an empty Year from the year already known for the row's ID,
// else from TMDB's details for it.
func (p *Provider) Dated(r media.SearchResult) media.SearchResult {
	if r.Year != "" || !IsID(r.ID) {
		return r
	}
	p.mu.Lock()
	known := p.rows[r.ID].Year
	p.mu.Unlock()
	if known != "" {
		r.Year = known
		return r
	}
	if p.client == nil {
		return r
	}
	if d, err := p.client.Details(r.ID); err == nil {
		r.Year = d.Year
	}
	return r
}

func (p *Provider) GetSeasons(id string) ([]media.Season, error) {
	pid, err := p.providerID(id)
	if err != nil {
		return nil, err
	}
	return p.Provider.GetSeasons(pid)
}

func (p *Provider) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	pid, err := p.providerID(id)
	if err != nil {
		return nil, err
	}
	return p.Provider.GetEpisodes(pid, seasonID)
}

// Trending returns the catalogue's trending (or anime) list, or the
// provider's when there is no key or the catalogue fails. Any notice is left
// pending for TakeNotice.
func (p *Provider) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	res, notice, err := p.TrendingWithNotice(mt)
	if notice != "" {
		p.setNotice(notice)
	}
	return res, err
}

// TrendingWithNotice is Trending with the notice returned alongside the list,
// for callers that fetch on the same Provider concurrently.
func (p *Provider) TrendingWithNotice(mt media.MediaType) ([]media.SearchResult, string, error) {
	if p.client == nil {
		res, err := p.Provider.Trending(mt)
		return res, p.hintOnce(), err
	}
	var res []media.SearchResult
	var err error
	if p.kind == Anime {
		res, err = p.client.Anime()
	} else {
		res, err = p.client.Trending(mt)
	}
	if err != nil {
		fb, ferr := p.Provider.Trending(mt)
		if ferr != nil {
			return fb, "", ferr
		}
		return fb, fallbackNotice(err), nil
	}
	p.remember(res)
	return res, "", nil
}

// Recent returns the catalogue's recent list. The anime tab has no recent
// catalogue and always uses the provider.
func (p *Provider) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	if p.client == nil {
		if n := p.hintOnce(); n != "" {
			p.setNotice(n)
		}
		return p.Provider.Recent(mt)
	}
	if p.kind == Anime {
		return p.Provider.Recent(mt)
	}
	res, err := p.client.Recent(mt)
	if err != nil {
		fb, ferr := p.Provider.Recent(mt)
		if ferr == nil {
			p.setNotice(fallbackNotice(err))
		}
		return fb, ferr
	}
	p.remember(res)
	return res, nil
}

// GetDetails answers movie/<n> and tv/<n> ids from the catalogue and every
// other id from the provider.
func (p *Provider) GetDetails(id string) (*media.ContentDetail, error) {
	if p.client == nil || !IsID(id) {
		return p.Provider.GetDetails(id)
	}
	d, err := p.client.Details(id)
	if err != nil {
		pid, perr := p.providerID(id)
		if perr != nil {
			return nil, perr
		}
		return p.Provider.GetDetails(pid)
	}
	out := d.ContentDetail
	if d.IMDbID != "" {
		if out.Description != "" {
			out.Description += "\n\n"
		}
		out.Description += "IMDb: " + d.IMDbID
	}
	return &out, nil
}
