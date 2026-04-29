// Package dlmanager orchestrates download workers, queue management, and progress reporting.
package dlmanager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"lobster/internal/dlmanager/engine"
	"lobster/internal/dlmanager/store"
)

// ProgressUpdate carries download progress from a worker to the TUI.
type ProgressUpdate struct {
	DownloadID    int
	Status        string
	DoneBytes     int64
	TotalBytes    int64
	DoneSegments  int
	TotalSegments int
	Speed         float64 // bytes/sec rolling average
	Error         string
}

// Manager coordinates download workers and relays progress.
type Manager struct {
	store      *store.Store
	httpEngine engine.Engine
	hlsEngine  *engine.HLSEngine
	workers    int
	pollRate   time.Duration // how often workers poll for new work
	progress   chan ProgressUpdate
	notify     chan struct{} // signals workers that new work is available

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	cancels map[int]context.CancelFunc // per-download cancel funcs
}

// New creates a Manager. Call Start to begin processing.
func New(s *store.Store, httpEng engine.Engine, hlsEng *engine.HLSEngine, workers int) *Manager {
	return &Manager{
		store:      s,
		httpEngine: httpEng,
		hlsEngine:  hlsEng,
		workers:    workers,
		pollRate:   500 * time.Millisecond,
		progress:   make(chan ProgressUpdate, 100),
		notify:     make(chan struct{}, 1),
		cancels:    make(map[int]context.CancelFunc),
	}
}

// SetPollRate sets how often workers poll for new work (for testing).
func (m *Manager) SetPollRate(d time.Duration) {
	m.pollRate = d
}

// Start launches worker goroutines and recovers stale downloads.
func (m *Manager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Recover crashed downloads.
	if recovered, err := m.store.RecoverStale(60 * time.Second); err == nil {
		for _, d := range recovered {
			m.sendProgress(ProgressUpdate{
				DownloadID: d.ID,
				Status:     "paused",
			})
		}
	}

	for i := 0; i < m.workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
}

// Stop cancels all downloads and waits for workers to exit.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	close(m.progress)
}

// Progress returns the channel to read progress updates from.
func (m *Manager) Progress() <-chan ProgressUpdate {
	return m.progress
}

// Queue inserts a download into the store and notifies workers.
func (m *Manager) Queue(d *store.Download) (int, error) {
	if d.Status == "" {
		d.Status = "queued"
	}
	id, err := m.store.InsertDownload(d)
	if err != nil {
		return 0, err
	}
	m.poke()
	return id, nil
}

// Pause cancels the active download and sets status to paused.
func (m *Manager) Pause(id int) error {
	m.mu.Lock()
	cancelFn, ok := m.cancels[id]
	m.mu.Unlock()

	if ok {
		cancelFn()
	}
	return m.store.UpdateStatus(id, "paused", "")
}

// Resume sets a paused download back to queued and notifies workers.
func (m *Manager) Resume(id int) error {
	if err := m.store.UpdateStatus(id, "queued", ""); err != nil {
		return err
	}
	m.poke()
	return nil
}

// Cancel aborts a download and marks it failed.
func (m *Manager) Cancel(id int) error {
	m.mu.Lock()
	cancelFn, ok := m.cancels[id]
	m.mu.Unlock()

	if ok {
		cancelFn()
	}
	return m.store.UpdateStatus(id, "failed", "cancelled by user")
}

// Retry re-queues a failed download for another attempt.
func (m *Manager) Retry(id int) error {
	// Clear stream info so it gets re-resolved.
	if err := m.store.UpdateStreamInfo(id, "", "", ""); err != nil {
		return err
	}
	if err := m.store.UpdateStatus(id, "queued", ""); err != nil {
		return err
	}
	m.poke()
	return nil
}

// Remove deletes a download from the store.
func (m *Manager) Remove(id int) error {
	m.mu.Lock()
	cancelFn, ok := m.cancels[id]
	m.mu.Unlock()

	if ok {
		cancelFn()
	}
	return m.store.DeleteDownload(id)
}

// Store returns the underlying store for direct queries.
func (m *Manager) Store() *store.Store {
	return m.store
}

func (m *Manager) worker() {
	defer m.wg.Done()

	poll := time.NewTicker(m.pollRate)
	defer poll.Stop()

	for {
		// Atomically claim work.
		dl, err := m.store.ClaimNextQueued()
		if err != nil || dl == nil {
			// No work available, wait for notification or poll.
			select {
			case <-m.ctx.Done():
				return
			case <-m.notify:
				continue
			case <-poll.C:
				continue
			}
		}

		m.processDownload(dl)
		// After completing, poke to wake other workers in case more work queued up.
		m.poke()
	}
}

func (m *Manager) processDownload(dl *store.Download) {
	dlCtx, dlCancel := context.WithCancel(m.ctx)
	m.mu.Lock()
	m.cancels[dl.ID] = dlCancel
	m.mu.Unlock()

	defer func() {
		dlCancel()
		m.mu.Lock()
		delete(m.cancels, dl.ID)
		m.mu.Unlock()
	}()

	// Already claimed as "downloading" by ClaimNextQueued.
	m.sendProgress(ProgressUpdate{DownloadID: dl.ID, Status: "downloading"})

	var dlErr error
	switch dl.StreamType {
	case "http":
		dlErr = m.downloadHTTP(dlCtx, dl)
	case "hls":
		dlErr = m.downloadHLS(dlCtx, dl)
	default:
		dlErr = fmt.Errorf("unknown stream type: %q", dl.StreamType)
	}

	if dlErr != nil {
		if dlCtx.Err() != nil {
			// Cancelled/paused — don't overwrite status.
			return
		}
		m.store.UpdateStatus(dl.ID, "failed", dlErr.Error())
		m.sendProgress(ProgressUpdate{DownloadID: dl.ID, Status: "failed", Error: dlErr.Error()})
		return
	}

	m.store.UpdateStatus(dl.ID, "completed", "")
	m.sendProgress(ProgressUpdate{DownloadID: dl.ID, Status: "completed"})
}

func (m *Manager) downloadHTTP(ctx context.Context, dl *store.Download) error {
	tracker := newSpeedTracker()

	progressFn := func(done, total int64) {
		tracker.record(done)
		m.store.UpdateProgress(dl.ID, done, 0)
		m.sendProgress(ProgressUpdate{
			DownloadID: dl.ID,
			Status:     "downloading",
			DoneBytes:  done,
			TotalBytes: total,
			Speed:      tracker.speed(),
		})
	}

	// Check for existing partial download.
	if dl.DoneBytes > 0 {
		return m.httpEngine.Resume(ctx, dl.StreamURL, dl.OutputPath, dl.Referer, progressFn)
	}
	return m.httpEngine.Download(ctx, dl.StreamURL, dl.OutputPath, dl.Referer, progressFn)
}

func (m *Manager) downloadHLS(ctx context.Context, dl *store.Download) error {
	tracker := newSpeedTracker()

	progressFn := func(doneSegs, totalSegs int64) {
		tracker.record(doneSegs)
		m.store.UpdateProgress(dl.ID, 0, int(doneSegs))
		m.sendProgress(ProgressUpdate{
			DownloadID:    dl.ID,
			Status:        "downloading",
			DoneSegments:  int(doneSegs),
			TotalSegments: int(totalSegs),
			Speed:         tracker.speed(),
		})
	}

	if dl.DoneSegments > 0 {
		return m.hlsEngine.ResumeWithID(ctx, dl.ID, dl.StreamURL, dl.OutputPath, dl.Referer, progressFn)
	}
	return m.hlsEngine.ResumeWithID(ctx, dl.ID, dl.StreamURL, dl.OutputPath, dl.Referer, progressFn)
}

func (m *Manager) sendProgress(p ProgressUpdate) {
	select {
	case m.progress <- p:
	default:
		// Drop update if channel is full to avoid blocking workers.
	}
}

func (m *Manager) poke() {
	// Drain and re-fill to ensure workers see the signal.
	select {
	case <-m.notify:
	default:
	}
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// speedTracker computes a rolling average speed over recent samples.
type speedTracker struct {
	mu      sync.Mutex
	samples []speedSample
}

type speedSample struct {
	at    time.Time
	value int64
}

func newSpeedTracker() *speedTracker {
	return &speedTracker{}
}

func (s *speedTracker) record(value int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.samples = append(s.samples, speedSample{at: now, value: value})

	// Keep only last 5 seconds of samples.
	cutoff := now.Add(-5 * time.Second)
	start := 0
	for start < len(s.samples) && s.samples[start].at.Before(cutoff) {
		start++
	}
	if start > 0 && start < len(s.samples) {
		s.samples = s.samples[start:]
	}
}

func (s *speedTracker) speed() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) < 2 {
		return 0
	}
	first := s.samples[0]
	last := s.samples[len(s.samples)-1]
	elapsed := last.at.Sub(first.at).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(last.value-first.value) / elapsed
}
