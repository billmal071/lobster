package resolver

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
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
	saveMu  sync.Mutex
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
	ranked := make([]provider.Provider, len(providers))
	copy(ranked, providers)

	// Snapshot the comparison keys UNDER the lock so the sort comparator never
	// reads the shared records map/structs (concurrent map read+write is a fatal
	// runtime panic, not just a race).
	type sortKey struct {
		score float64
		lat   int
	}
	keys := make(map[string]sortKey, len(providers))
	h.mu.RLock()
	for _, p := range providers {
		name := ProviderName(p)
		rec := h.records[name]
		lat := 1 << 30
		if rec != nil {
			lat = rec.LatencyMs
		}
		keys[name] = sortKey{score: effectiveScore(rec, now), lat: lat}
	}
	h.mu.RUnlock()

	sort.SliceStable(ranked, func(i, j int) bool {
		ki := keys[ProviderName(ranked[i])]
		kj := keys[ProviderName(ranked[j])]
		if ki.score != kj.score {
			return ki.score > kj.score
		}
		return ki.lat < kj.lat
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

// LoadHealth reads the health file if present; on any error it returns an
// empty, usable store with the path set so Save() can persist later.
func LoadHealth(path string) *HealthStore {
	h := &HealthStore{records: make(map[string]*ProviderHealth), path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var recs map[string]*ProviderHealth
	if json.Unmarshal(data, &recs) == nil && recs != nil {
		h.records = recs
	}
	return h
}

// Save atomically writes the store to its path (temp file + rename). The records
// are snapshotted and marshalled under the records lock, then file I/O happens
// outside it (serialized by saveMu) so a slow disk write can't block concurrent
// Record calls. No-op when path is empty.
func (h *HealthStore) Save() error {
	h.mu.RLock()
	path := h.path
	if path == "" {
		h.mu.RUnlock()
		return nil
	}
	data, err := json.MarshalIndent(h.records, "", "  ")
	h.mu.RUnlock()
	if err != nil {
		return err
	}
	// Disk I/O outside the records lock; saveMu serializes concurrent file writes
	// so two resolves can't clobber the temp file or race the rename.
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
