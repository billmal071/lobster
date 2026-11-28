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
	if w, ok := p.(*catalog.Provider); ok {
		p = w.Unwrap()
	}
	w := catalog.Wrap(p, configuredCatalogClient(), catalog.General)
	r, err := datedCatalogueRow(w, r)
	if err != nil {
		return r, err
	}
	r.ProviderID = ""
	return w.ResolveRow(r), nil
}

func datedCatalogueRow(w *catalog.Provider, r media.SearchResult) (media.SearchResult, error) {
	if !r.FromCatalogue || r.Year != "" {
		return r, nil
	}
	r = w.Dated(r)
	if r.Year == "" {
		return r, fmt.Errorf("cannot tell which %q to play: the TMDB catalogue gives no release year for %s", r.Title, r.ID)
	}
	return r, nil
}

func configuredCatalogClient() *catalog.Client {
	if cfg == nil {
		return nil
	}
	return newCatalogClient(cfg.TMDBAPIKey)
}
