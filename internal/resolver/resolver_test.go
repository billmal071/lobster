package resolver

import (
	"context"
	"errors"
	"testing"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
)

// delayedSP embeds fakeSP; Search returns one result, Watch sleeps delay then
// returns stream or err.
type delayedSP struct {
	*fakeSP
	name   string
	delay  time.Duration
	stream *media.Stream
	err    error
}

func (d *delayedSP) Search(_ string) ([]media.SearchResult, error) {
	return []media.SearchResult{{ID: "movie/1", Title: "Foo", Type: media.Movie}}, nil
}

func (d *delayedSP) Watch(_, _, _, _ string) (*media.Stream, error) {
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	return d.stream, d.err
}

func TestResolveFirstValidWins(t *testing.T) {
	slow := &delayedSP{fakeSP: &fakeSP{}, name: "slow", delay: 300 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/slow.m3u8"}}
	fast := &delayedSP{fakeSP: &fakeSP{}, name: "fast", delay: 10 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/fast.m3u8"}}
	r := New([]provider.Provider{slow, fast}, NewHealthStore(), func(string, ...any) {})
	r.validate = false
	r.batchSize = 3 // race both together
	got, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err != nil || got == nil {
		t.Fatalf("expected a stream, got %v / %v", got, err)
	}
	if got.URL != "https://cdn/fast.m3u8" {
		t.Fatalf("expected fastest provider to win, got %s", got.URL)
	}
	if rep == nil {
		t.Fatal("expected a report")
	}
}

func TestResolveAllFailReturnsReport(t *testing.T) {
	a := &delayedSP{fakeSP: &fakeSP{}, name: "a", err: errors.New("no matching result")}
	b := &delayedSP{fakeSP: &fakeSP{}, name: "b", err: errors.New("status 404")}
	r := New([]provider.Provider{a, b}, NewHealthStore(), func(string, ...any) {})
	r.validate = false
	got, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if got != nil || err == nil {
		t.Fatalf("expected failure, got %v", got)
	}
	if rep == nil || len(rep.Attempts) != 2 {
		t.Fatalf("expected 2 attempts in report, got %+v", rep)
	}
}

func TestResolveAdvancesPastSlowBatch(t *testing.T) {
	slow := &delayedSP{fakeSP: &fakeSP{}, name: "slow", delay: 500 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/slow.m3u8"}}
	fast := &delayedSP{fakeSP: &fakeSP{}, name: "fast", delay: 5 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/fast.m3u8"}}
	r := New([]provider.Provider{slow, fast}, NewHealthStore(), func(string, ...any) {})
	r.validate = false
	r.batchSize = 1               // slow in batch 1, fast in batch 2
	r.attemptTimeout = 50 * time.Millisecond // batch 1 can't finish in time -> abandon -> batch 2
	start := time.Now()
	got, _, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err != nil || got == nil || got.URL != "https://cdn/fast.m3u8" {
		t.Fatalf("expected fast (batch 2) to win after slow batch abandoned, got %v / %v", got, err)
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("resolver waited for the slow provider (%v) instead of advancing after the batch deadline", elapsed)
	}
}

// A provider that exceeds the batch deadline must be recorded as a FAILURE (so a
// chronically-slow provider is deprioritized), not rewarded by a late
// self-reported success, and must appear in the report.
func TestResolveRecordsTimeoutAsFailure(t *testing.T) {
	slow := &delayedSP{fakeSP: &fakeSP{}, name: "slow", delay: 200 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/slow.m3u8"}}
	h := NewHealthStore()
	r := New([]provider.Provider{slow}, h, func(string, ...any) {})
	r.validate = false
	r.batchSize = 1
	r.attemptTimeout = 20 * time.Millisecond // slower than 20ms -> abandoned as timeout
	_, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err == nil {
		t.Fatal("expected failure when the only provider is too slow")
	}
	rec := h.records[ProviderName(slow)]
	if rec == nil || rec.Score >= healthNeutral {
		t.Fatalf("expected slow provider recorded as failure (score < %.2f), got %+v", healthNeutral, rec)
	}
	foundTimeout := false
	for _, a := range rep.Attempts {
		if a.Stage == "batch-timeout" && a.Provider == ProviderName(slow) {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatalf("expected a batch-timeout attempt for the slow provider in the report, got %+v", rep.Attempts)
	}
}

// The attempt Resolve appends when its own overall deadline expires must be
// the one IsProviderProbe rejects.
//
// This pins a producer against a consumer in another package. cmd's
// resolveFailure.providersTried counts providers for the "%d providers tried"
// line in a user-facing failure message, and it skips this row by asking
// IsProviderProbe. Were Resolve to record the row under some other name — a
// rename, a second synthetic row added for a different reason — nothing would
// fail to compile and the count would silently gain one. So the assertion is
// not "a timeout row exists" but "the timeout row Resolve actually writes is
// not a provider probe, and every real probe still is".
func TestResolveOverallTimeoutRowIsNotAProviderProbe(t *testing.T) {
	slow := &delayedSP{fakeSP: &fakeSP{}, name: "slow", delay: time.Second, stream: &media.Stream{URL: "https://cdn/slow.m3u8"}}
	r := New([]provider.Provider{slow}, NewHealthStore(), func(string, ...any) {})
	r.validate = false
	r.batchSize = 1
	r.attemptTimeout = time.Second
	// Shorter than the batch deadline, so the overall deadline is what fires.
	r.overallTimeout = 20 * time.Millisecond

	_, rep, err := r.Resolve(context.Background(), Request{Title: "Foo", MediaType: media.Movie})
	if err == nil {
		t.Fatal("expected failure when the overall deadline expires")
	}

	var synthetic, probes int
	for _, a := range rep.Attempts {
		if a.IsProviderProbe() {
			probes++
			continue
		}
		synthetic++
		if a.Stage != StageOverallTimeout {
			t.Errorf("non-probe attempt has Stage %q, want %q", a.Stage, StageOverallTimeout)
		}
	}
	if synthetic != 1 {
		t.Fatalf("IsProviderProbe rejected %d of %d attempts, want exactly 1 (the overall-timeout row); attempts: %+v",
			synthetic, len(rep.Attempts), rep.Attempts)
	}
	// The abandoned provider is still a probe, so the count a caller derives
	// from IsProviderProbe is 1 here and not 0.
	if probes != 1 {
		t.Errorf("IsProviderProbe accepted %d attempts, want 1 (the abandoned provider); attempts: %+v", probes, rep.Attempts)
	}
}
