package resolver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"lobster/internal/httputil"
	"lobster/internal/media"
	"lobster/internal/provider"
)

// Resolver holds the shared configuration used by probe and the racing Resolve
// method.
type Resolver struct {
	providers      []provider.Provider
	health         *HealthStore
	client         *http.Client
	batchSize      int
	attemptTimeout time.Duration
	overallTimeout time.Duration
	log            func(string, ...any)
	validate       bool
}

// New constructs a Resolver with sensible defaults.
// nil log is replaced with a no-op.
func New(providers []provider.Provider, health *HealthStore, log func(string, ...any)) *Resolver {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Resolver{
		providers:      providers,
		health:         health,
		log:            log,
		batchSize:      3,
		attemptTimeout: 30 * time.Second,
		overallTimeout: 90 * time.Second,
		client:         httputil.NewClient(),
		validate:       true,
	}
}

// Report records every probe attempt made during a Resolve call.
type Report struct {
	Attempts []Attempt
}

// Attempt is a single probe result recorded in a Report.
type Attempt struct {
	Provider   string
	Stage      string
	Err        error
	DurationMs int64
}

// Summary returns a short human-readable summary of the report.
// Task 10 provides the real implementation; this stub keeps the package compiling.
func (rep *Report) Summary() string { return "" }

// add appends a probeResult to the report.
func (rep *Report) add(res probeResult) {
	rep.Attempts = append(rep.Attempts, Attempt{
		Provider:   res.Provider,
		Stage:      res.Stage,
		Err:        res.Err,
		DurationMs: res.Latency.Milliseconds(),
	})
}

// Resolve runs staged racing across health-ordered provider batches.
// The first probe that returns a valid stream wins; the rest are abandoned
// (not cancelled — each goroutine's send is unblocked by the K-buffered channel).
func (r *Resolver) Resolve(ctx context.Context, req Request) (*media.Stream, *Report, error) {
	ctx, cancel := context.WithTimeout(ctx, r.overallTimeout)
	defer cancel()

	report := &Report{}
	batches := r.health.Order(r.providers, time.Now(), r.batchSize)

	for _, batch := range batches {
		// Buffered to exactly len(batch) so abandoned-loser goroutines never
		// block on send when the racer returns early.
		ch := make(chan probeResult, len(batch))
		for _, p := range batch {
			p := p
			go func() { ch <- r.probe(p, req) }()
		}

		remaining := len(batch)
		for remaining > 0 {
			select {
			case <-ctx.Done():
				report.add(probeResult{Stage: "overall-timeout", Err: ctx.Err()})
				_ = r.health.Save()
				return nil, report, ctx.Err()
			case res := <-ch:
				remaining--
				report.add(res)
				if res.Err == nil && res.Stream != nil {
					_ = r.health.Save()
					return res.Stream, report, nil // abandon the rest
				}
			}
		}
	}

	_ = r.health.Save()
	return nil, report, fmt.Errorf("all providers failed: %s", report.Summary())
}
