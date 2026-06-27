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
	slow := &delayedSP{name: "slow", delay: 500 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/slow.m3u8"}}
	fast := &delayedSP{name: "fast", delay: 5 * time.Millisecond, stream: &media.Stream{URL: "https://cdn/fast.m3u8"}}
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
