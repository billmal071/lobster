package resolver

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"lobster/internal/provider"
)

const (
	healthAlpha         = 0.3
	healthNeutral       = 0.5
	healthHalfLifeHours = 72.0
)

// ProviderName returns the short type name of a provider, e.g. "MovieBox".
func ProviderName(p provider.Provider) string {
	full := fmt.Sprintf("%T", p)
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

type ProviderHealth struct {
	Score       float64   `json:"score"`
	LatencyMs   int       `json:"latencyMs"`
	LastSuccess time.Time `json:"lastSuccess"`
	LastFailure time.Time `json:"lastFailure"`
	Samples     int       `json:"samples"`
}

type HealthStore struct {
	mu      sync.RWMutex
	records map[string]*ProviderHealth
	path    string // set by LoadHealth (Task 4); empty => no persistence
}

func NewHealthStore() *HealthStore {
	return &HealthStore{records: make(map[string]*ProviderHealth)}
}

// Record folds one outcome into the provider's EWMA score and latency.
func (h *HealthStore) Record(name string, success bool, latency time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := h.records[name]
	if rec == nil {
		rec = &ProviderHealth{Score: healthNeutral}
		h.records[name] = rec
	}
	target := 0.0
	if success {
		target = 1.0
	}
	rec.Score = healthAlpha*target + (1-healthAlpha)*rec.Score
	if success {
		ms := int(latency.Milliseconds())
		if rec.LatencyMs == 0 {
			rec.LatencyMs = ms
		} else {
			rec.LatencyMs = int(healthAlpha*float64(ms) + (1-healthAlpha)*float64(rec.LatencyMs))
		}
		rec.LastSuccess = time.Now()
	} else {
		rec.LastFailure = time.Now()
	}
	rec.Samples++
}

// effectiveScore decays the stored score toward neutral as the last activity ages,
// so a provider that failed days ago is retried rather than permanently demoted.
func effectiveScore(rec *ProviderHealth, now time.Time) float64 {
	if rec == nil {
		return healthNeutral
	}
	last := rec.LastSuccess
	if rec.LastFailure.After(last) {
		last = rec.LastFailure
	}
	if last.IsZero() {
		return rec.Score
	}
	ageHours := now.Sub(last).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	decay := math.Pow(0.5, ageHours/healthHalfLifeHours)
	return healthNeutral + (rec.Score-healthNeutral)*decay
}

// Order ranks providers by decayed score (desc), tie-break latency (asc),
// then chunks them into batches of batchSize (>=1).
func (h *HealthStore) Order(providers []provider.Provider, now time.Time, batchSize int) [][]provider.Provider {
	if batchSize < 1 {
		batchSize = 1
	}
	h.mu.RLock()
	ranked := make([]provider.Provider, len(providers))
	copy(ranked, providers)
	score := func(p provider.Provider) float64 { return effectiveScore(h.records[ProviderName(p)], now) }
	lat := func(p provider.Provider) int {
		if r := h.records[ProviderName(p)]; r != nil {
			return r.LatencyMs
		}
		return 1 << 30
	}
	h.mu.RUnlock()
	sort.SliceStable(ranked, func(i, j int) bool {
		si, sj := score(ranked[i]), score(ranked[j])
		if si != sj {
			return si > sj
		}
		return lat(ranked[i]) < lat(ranked[j])
	})
	var batches [][]provider.Provider
	for i := 0; i < len(ranked); i += batchSize {
		end := i + batchSize
		if end > len(ranked) {
			end = len(ranked)
		}
		batches = append(batches, ranked[i:end])
	}
	return batches
}
