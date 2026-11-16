package catalog

import (
	"errors"
	"fmt"
	"sync"

	"lobster/internal/media"
	"lobster/internal/provider"
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

func (p *Provider) hintOnce() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kind == General && !p.hintDone {
		p.hintDone = true
		p.notice = Hint
	}
}

func (p *Provider) fellBack(err error) {
	const tail = "; showing the provider's list instead"
	switch {
	case errors.Is(err, ErrKeyRejected):
		p.setNotice(ErrKeyRejected.Error() + tail)
	case errors.Is(err, ErrRateLimited):
		p.setNotice(ErrRateLimited.Error() + tail)
	default:
		p.setNotice(fmt.Sprintf("TMDB unavailable (%v)%s", err, tail))
	}
}

// Trending returns the catalogue's trending (or anime) list, or the
// provider's when there is no key or the catalogue fails.
func (p *Provider) Trending(mt media.MediaType) ([]media.SearchResult, error) {
	if p.client == nil {
		p.hintOnce()
		return p.Provider.Trending(mt)
	}
	var res []media.SearchResult
	var err error
	if p.kind == Anime {
		res, err = p.client.Anime()
	} else {
		res, err = p.client.Trending(mt)
	}
	if err != nil {
		p.fellBack(err)
		return p.Provider.Trending(mt)
	}
	return res, nil
}

// Recent returns the catalogue's recent list. The anime tab has no recent
// catalogue and always uses the provider.
func (p *Provider) Recent(mt media.MediaType) ([]media.SearchResult, error) {
	if p.client == nil {
		p.hintOnce()
		return p.Provider.Recent(mt)
	}
	if p.kind == Anime {
		return p.Provider.Recent(mt)
	}
	res, err := p.client.Recent(mt)
	if err != nil {
		p.fellBack(err)
		return p.Provider.Recent(mt)
	}
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
		p.fellBack(err)
		return p.Provider.GetDetails(id)
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
