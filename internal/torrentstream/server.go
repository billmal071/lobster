// Package torrentstream plays a BitTorrent magnet without waiting for the
// download to finish. It prioritises pieces in reading order and serves the
// chosen file over loopback HTTP, so a player opens an ordinary URL and seeks
// normally while the rest arrives.
//
// Note this makes the process a swarm participant: it announces to trackers and
// uploads pieces to peers, which an HTTP stream never does.
package torrentstream

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/storage"
)

// readahead is how far beyond the play head to prioritise. Large enough to
// absorb a stall on a thin swarm, small enough that startup does not wait on it.
const readahead = 24 << 20

// InfoTimeout bounds the wait for torrent metadata. A magnet with no reachable
// peers otherwise hangs forever with no explanation.
const InfoTimeout = 90 * time.Second

// Server streams one or more magnets over loopback.
type Server struct {
	client *torrent.Client
	srv    *http.Server
	ln     net.Listener
	base   string
	dir    string
	// tmpDir is removed on Close when the data directory was not user-chosen.
	tmpDir string

	// entries is read by HTTP handler goroutines while Serve writes it, so it
	// needs the mutex even though writes only happen at stream setup.
	mu      sync.RWMutex
	entries map[string]*serveEntry
}

type serveEntry struct {
	file *torrent.File
	name string
}

// is32Bit reports a 32-bit build, where the storage layer cannot mmap a
// multi-gigabyte film.
const is32Bit = ^uint(0)>>32 == 0

// fileIoEnv selects the storage layer's file backend. It is read in that
// package's init(), so it can only be set before the process starts — not from
// here.
const fileIoEnv = "TORRENT_STORAGE_DEFAULT_FILE_IO"

// New starts a torrent client and a loopback HTTP server. dataDir is where
// pieces land; empty uses a temp directory removed on Close.
func New(dataDir string) (*Server, error) {
	// A 32-bit build cannot map a 2 GB file, and the failure surfaces deep in
	// the library as "mapping file: invalid argument" after the download has
	// apparently started — worth catching up front with the two things that
	// actually work, both verified.
	if is32Bit && os.Getenv(fileIoEnv) != "classic" {
		return nil, fmt.Errorf(
			"torrent streaming needs a 64-bit build: this one is 32-bit and cannot memory-map a multi-gigabyte file.\n"+
				"Either rebuild with GOARCH=amd64, or re-run with %s=classic", fileIoEnv)
	}
	tmp := ""
	if dataDir == "" {
		d, err := os.MkdirTemp("", "lobster-torrent-")
		if err != nil {
			return nil, err
		}
		dataDir, tmp = d, d
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.DefaultStorage = storage.NewFile(dataDir)
	// Seeding is what turns watching into distributing. Leave it off by default;
	// the swarm still sees this peer while it leeches, so this is not anonymity,
	// only a smaller footprint.
	cfg.Seed = false
	cfg.NoUpload = true

	client, err := torrent.NewClient(cfg)
	if err != nil {
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
		return nil, fmt.Errorf("starting torrent client: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		if tmp != "" {
			_ = os.RemoveAll(tmp)
		}
		return nil, err
	}

	s := &Server{
		client:  client,
		ln:      ln,
		base:    fmt.Sprintf("http://%s", ln.Addr().String()),
		dir:     dataDir,
		tmpDir:  tmp,
		entries: make(map[string]*serveEntry),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stream/", s.handle)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Serve adds a magnet and returns a loopback URL for its main video file. It
// blocks until metadata arrives, which is when the file list becomes known.
func (s *Server) Serve(magnet string) (string, error) {
	t, err := s.client.AddMagnet(magnet)
	if err != nil {
		return "", fmt.Errorf("adding magnet: %w", err)
	}

	select {
	case <-t.GotInfo():
	case <-time.After(InfoTimeout):
		return "", fmt.Errorf("no peers answered within %s — the swarm may be dead", InfoTimeout)
	}

	tfiles := t.Files()
	infos := make([]fileInfo, len(tfiles))
	for i, f := range tfiles {
		infos[i] = fileInfo{path: f.DisplayPath(), length: f.Length()}
	}
	idx, err := pickVideo(infos)
	if err != nil {
		return "", err
	}
	file := tfiles[idx]

	// Only the chosen file is wanted: a torrent with extras would otherwise
	// spend the thin early bandwidth on files nobody is watching.
	for _, f := range tfiles {
		f.SetPriority(torrent.PiecePriorityNone)
	}
	file.SetPriority(torrent.PiecePriorityNormal)

	key := t.InfoHash().HexString()
	s.mu.Lock()
	s.entries[key] = &serveEntry{file: file, name: filepath.Base(file.DisplayPath())}
	s.mu.Unlock()
	return fmt.Sprintf("%s/stream/%s", s.base, key), nil
}

// lookup resolves a stream key under the read lock.
func (s *Server) lookup(key string) (*serveEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[key]
	return e, ok
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/stream/")
	entry, ok := s.lookup(key)
	if !ok {
		http.Error(w, "unknown stream", http.StatusNotFound)
		return
	}

	reader := entry.file.NewReader()
	defer func() { _ = reader.Close() }()
	// Readahead is what makes this a stream rather than a random-access download:
	// it tells the client to fetch ahead of the play head instead of on demand.
	reader.SetReadahead(readahead)
	reader.SetResponsive()

	// ServeContent gives Range, 206 and 416 handling, so the player can seek.
	http.ServeContent(w, r, entry.name, time.Time{}, reader)
}

// Close stops the HTTP server and the torrent client, and removes the temporary
// data directory when one was created.
func (s *Server) Close() error {
	if s.srv != nil {
		_ = s.srv.Close()
	}
	if s.client != nil {
		s.client.Close()
	}
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
	return nil
}

// IsMagnet reports whether a stream URL is a magnet rather than an HTTP stream.
func IsMagnet(u string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "magnet:")
}
