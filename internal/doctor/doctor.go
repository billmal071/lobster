// Package doctor probes each content provider and reports which stage of the
// pipeline fails.
//
// "The provider is broken" is what a user already knows. Which stage broke is
// what makes a repair findable: a provider whose search works but whose embeds
// fail has moved its player and needs new extraction work, while one that fails
// at search has usually just moved a domain or renamed a field — a cheap fix,
// and invisible unless something reports the difference.
package doctor

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"lobster/internal/provider"
)

// Stages of the resolution pipeline, in the order they run.
const (
	StageSearch  = "search"
	StageServers = "servers"
	StageEmbed   = "embed"
	StageWatch   = "watch"
	StageOK      = "ok"
)

// Result is one provider's diagnosis.
type Result struct {
	Provider string
	Stage    string // the stage that failed, or StageOK
	OK       bool
	Detail   string // what happened, in the provider's own words where possible
	Latency  time.Duration
}

// Check runs a provider through the pipeline against query and reports the
// first stage that fails.
//
// It deliberately stops at an embed URL rather than extracting: extraction is a
// separate subsystem, and probing it would turn doctor into a playback test
// that is slow and fails for unrelated reasons.
func Check(name string, p provider.Provider, query string) Result {
	start := time.Now()
	res := Result{Provider: name}
	finish := func(stage, detail string, ok bool) Result {
		res.Stage, res.Detail, res.OK = stage, detail, ok
		res.Latency = time.Since(start)
		return res
	}

	results, err := p.Search(query)
	if err != nil {
		return finish(StageSearch, err.Error(), false)
	}
	if len(results) == 0 {
		// Distinct from an error: the endpoint answered, so the parse or the
		// query shape is what is wrong, which is a much cheaper fix.
		return finish(StageSearch, "no results (endpoint answered but nothing parsed)", false)
	}
	pick := results[0]

	if sp, ok := p.(provider.StreamProvider); ok {
		stream, err := sp.Watch(pick.ID, "", "Default", "1080")
		if err != nil {
			return finish(StageWatch, err.Error(), false)
		}
		if stream == nil || stream.URL == "" {
			return finish(StageWatch, "no stream URL returned", false)
		}
		return finish(StageOK, fmt.Sprintf("%d results, stream resolved", len(results)), true)
	}

	servers, err := p.GetServers(pick.ID, "")
	if err != nil {
		return finish(StageServers, err.Error(), false)
	}
	if len(servers) == 0 {
		return finish(StageServers, "no servers offered", false)
	}

	var lastErr string
	for _, srv := range servers {
		embed, err := p.GetEmbedURL(srv.ID)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if embed == "" {
			lastErr = "empty embed URL"
			continue
		}
		return finish(StageOK,
			fmt.Sprintf("%d results, %d servers, embed via %s", len(results), len(servers), srv.Name), true)
	}
	return finish(StageEmbed, fmt.Sprintf("all %d servers failed: %s", len(servers), lastErr), false)
}

// Named pairs a display name with a provider, and optionally its own probe
// query. Anime providers index a different catalogue, so probing them with a
// live-action title reports "broken" for a provider that is working fine.
type Named struct {
	Name     string
	Provider provider.Provider
	Query    string // empty uses the shared query
}

// CheckAll probes every provider concurrently and returns the results ordered
// healthy-first, then by name, so the usable sources are what you read first.
func CheckAll(providers []Named, query string) []Result {
	results := make([]Result, len(providers))
	var wg sync.WaitGroup
	for i, np := range providers {
		wg.Add(1)
		go func(idx int, n Named) {
			defer wg.Done()
			q := n.Query
			if q == "" {
				q = query
			}
			results[idx] = Check(n.Name, n.Provider, q)
		}(i, np)
	}
	wg.Wait()

	sort.SliceStable(results, func(a, b int) bool {
		if results[a].OK != results[b].OK {
			return results[a].OK
		}
		return results[a].Provider < results[b].Provider
	})
	return results
}

// Format renders results as an aligned report.
func Format(results []Result) string {
	width := 0
	for _, r := range results {
		if len(r.Provider) > width {
			width = len(r.Provider)
		}
	}
	var b strings.Builder
	healthy := 0
	for _, r := range results {
		mark, stage := "FAIL", r.Stage
		if r.OK {
			mark, stage = "ok", ""
			healthy++
		}
		fmt.Fprintf(&b, "  %-4s %-*s  %6s  %-8s %s\n",
			mark, width, r.Provider, r.Latency.Round(time.Millisecond), stage, r.Detail)
	}
	fmt.Fprintf(&b, "\n%d of %d providers usable.\n", healthy, len(results))
	return b.String()
}
