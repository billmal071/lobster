// Package tbcpl reads TBCPL's (tbcpl.lol) static site catalog and exposes it
// as filterable entries for lobster's provider/fallback machinery.
package tbcpl

import "encoding/json"

// Site is one streaming site listed by TBCPL.
type Site struct {
	Name     string
	URL      string
	Category string // category id: "movies", "anime", "livetv", ...
	Status   string // "trusted", "new", or ""
	Enabled  bool
}

// Catalog is a flattened view of all sites across all categories.
type Catalog struct {
	Sites []Site
}

type rawCatalog struct {
	Categories []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Sites []struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Status  string `json:"status"`
			Enabled bool   `json:"enabled"`
		} `json:"sites"`
	} `json:"categories"`
}

// Parse turns TBCPL links.json bytes into a flattened Catalog.
func Parse(data []byte) (*Catalog, error) {
	var raw rawCatalog
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	c := &Catalog{}
	for _, cat := range raw.Categories {
		for _, s := range cat.Sites {
			c.Sites = append(c.Sites, Site{
				Name:     s.Name,
				URL:      s.URL,
				Category: cat.ID,
				Status:   s.Status,
				Enabled:  s.Enabled,
			})
		}
	}
	return c, nil
}

// ByCategory returns all sites in the given category id.
func (c *Catalog) ByCategory(id string) []Site {
	var out []Site
	for _, s := range c.Sites {
		if s.Category == id {
			out = append(out, s)
		}
	}
	return out
}

// Trusted returns only enabled sites flagged status=="trusted".
func (c *Catalog) Trusted() []Site {
	return c.EligibleSites(false)
}

// EligibleSites returns the sites eligible to participate in the feed: always
// excluding disabled sites, and — unless includeUntrusted is true — keeping only
// sites flagged status=="trusted". This is the single eligibility filter shared
// by every catalog consumer (mirror-domain matching and the embed provider) so
// they never diverge.
func (c *Catalog) EligibleSites(includeUntrusted bool) []Site {
	var out []Site
	for _, s := range c.Sites {
		if !s.Enabled {
			continue
		}
		if !includeUntrusted && s.Status != "trusted" {
			continue
		}
		out = append(out, s)
	}
	return out
}
