package cmd

import (
	"sync"
	"testing"

	"lobster/internal/config"
	"lobster/internal/tbcpl"
)

// seedTBCPLCatalog pins the process-wide TBCPL catalog singleton to a fixture
// and marks its sync.Once consumed, so tbcplCatalog() returns the fixture
// without reading the user cache or fetching the public catalog. Without this,
// any test that touches tbcplCatalog() with TBCPLFeed enabled depends on
// external state for both its result and its duration.
//
// Pass a nil catalog to simulate "feed loaded nothing".
func seedTBCPLCatalog(t *testing.T, c *config.Config, cat *tbcpl.Catalog) {
	t.Helper()
	prevCfg := cfg
	cfg = c
	tbcplCatOnce = sync.Once{}
	tbcplCatVal = cat
	tbcplCatOnce.Do(func() {}) // consume the Once so the loader never runs
	t.Cleanup(func() {
		cfg = prevCfg
		tbcplCatOnce = sync.Once{}
		tbcplCatVal = nil
	})
}
