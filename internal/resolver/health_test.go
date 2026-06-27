package resolver

import (
	"testing"
	"time"

	"lobster/internal/provider"
)

func TestRecordEWMA(t *testing.T) {
	h := NewHealthStore()
	h.Record("A", true, 100*time.Millisecond)  // 0.5 -> 0.3*1 + 0.7*0.5 = 0.65
	h.Record("A", true, 100*time.Millisecond)  // 0.3 + 0.7*0.65 = 0.755
	got := h.records["A"].Score
	if got < 0.75 || got > 0.76 {
		t.Fatalf("score after two successes = %.4f, want ~0.755", got)
	}
	h2 := NewHealthStore()
	h2.Record("B", false, 0) // 0.3*0 + 0.7*0.5 = 0.35
	if s := h2.records["B"].Score; s < 0.34 || s > 0.36 {
		t.Fatalf("score after one failure = %.4f, want ~0.35", s)
	}
}

func TestEffectiveScoreDecays(t *testing.T) {
	now := time.Unix(1_000_000_000, 0)
	rec := &ProviderHealth{Score: 0.1, LastFailure: now.Add(-72 * time.Hour)} // one half-life ago
	// effective = 0.5 + (0.1-0.5)*0.5 = 0.3
	got := effectiveScore(rec, now)
	if got < 0.29 || got > 0.31 {
		t.Fatalf("decayed score = %.4f, want ~0.3", got)
	}
	// No record -> neutral.
	if n := effectiveScore(nil, now); n != healthNeutral {
		t.Fatalf("nil record effective = %.4f, want %.4f", n, healthNeutral)
	}
}

func TestOrderRanksHealthyFirstAndBatches(t *testing.T) {
	h := NewHealthStore()
	now := time.Unix(1_000_000_000, 0)
	good := provider.NewSoap2Day()
	bad := provider.NewMovieBox()
	h.records[ProviderName(good)] = &ProviderHealth{Score: 0.9, LastSuccess: now}
	h.records[ProviderName(bad)] = &ProviderHealth{Score: 0.1, LastFailure: now}
	batches := h.Order([]provider.Provider{bad, good}, now, 1)
	if len(batches) != 2 || ProviderName(batches[0][0]) != ProviderName(good) {
		t.Fatalf("expected healthy provider first, got %v", batches)
	}
}
