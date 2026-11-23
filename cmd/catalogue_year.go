package cmd

import (
	"fmt"

	"lobster/internal/catalog"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// Providers match by title, which cannot tell a work from a same-titled remake without a year.
func identifyCatalogueRow(p provider.Provider, r media.SearchResult) (media.SearchResult, error) {
	if !r.FromCatalogue || r.Year != "" {
		return r, nil
	}
	y := catalogueYear(r.ID)
	if y == "" {
		return r, fmt.Errorf("cannot tell which %q to play: the TMDB catalogue gives no release year for %s", r.Title, r.ID)
	}
	r.Year = y
	if r.ProviderID != "" {
		r.ProviderID = ""
		if w, ok := p.(*catalog.Provider); ok {
			p = w.Unwrap()
		}
		r = resolveCatalogueRow(p, r)
	}
	return r, nil
}

func catalogueYear(id string) string {
	if cfg == nil || cfg.TMDBAPIKey == "" {
		return ""
	}
	d, err := newCatalogClient(cfg.TMDBAPIKey).Details(id)
	if err != nil {
		debugf("catalogue year for %s: %v", id, err)
		return ""
	}
	return d.Year
}
