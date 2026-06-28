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
		// pending tracks providers whose probe hasn't been received yet, so the
		// resolver can record a timeout for any it abandons at the batch deadline.
		pending := make(map[string]bool, len(batch))
		for _, p := range batch {
			p := p
			pending[ProviderName(p)] = true
			go func() { ch <- r.probe(p, req) }()
		}

		batchDeadline := time.After(r.attemptTimeout)
		remaining := len(batch)
		for remaining > 0 {
			select {
			case <-ctx.Done():
				r.recordPending(pending, report)
				report.add(probeResult{Provider: "(resolver)", Stage: "overall-timeout", Err: ctx.Err()})
				_ = r.health.Save()
				return nil, report, ctx.Err()
			case <-batchDeadline:
				// No winner within attemptTimeout: record the still-pending
				// providers as timeouts so chronically-slow providers are
				// deprioritized (not rewarded by a late self-reported success),
				// abandon their stragglers, and advance to the next batch.
				r.recordPending(pending, report)
				remaining = 0
			case res := <-ch:
				remaining--
				delete(pending, res.Provider)
				report.add(res)
				r.health.Record(res.Provider, res.Err == nil, res.Latency)
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

// recordPending marks every provider still running when its batch was abandoned
// (batch deadline or overall timeout) as a timeout failure, deprioritizing it
// for next time, and notes it in the report. Abandoned probe goroutines no
// longer self-report, so this is their only health signal.
func (r *Resolver) recordPending(pending map[string]bool, report *Report) {
	for name := range pending {
		r.health.Record(name, false, r.attemptTimeout)
		report.add(probeResult{
			Provider: name,
			Stage:    "batch-timeout",
			Err:      fmt.Errorf("no result within %s", r.attemptTimeout),
			Latency:  r.attemptTimeout,
		})
	}
}
