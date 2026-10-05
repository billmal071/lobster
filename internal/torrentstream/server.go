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
	"path"
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
	// data holds this run's pieces and is removed on Close.
	data runDir

	// warnf reports something the user should know that does not stop playback.
	// Never nil: New substitutes a no-op.
	warnf func(string, ...any)
	// wholeFile says the caller needs the entire chosen file on disk rather
	// than only the part that plays. It turns the free-space shortfall from a
	// warning into a refusal — see spaceAdvice.
	wholeFile bool

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

// Options configures a Server.
type Options struct {
	// DataDir is the user's configured torrent directory, empty for the
	// default. Either way the pieces land in a fresh subdirectory of it, which
	// Close removes — see newDataDir for where the default points and why.
	DataDir string

	// Warnf reports something the user should know but that does not stop
	// playback. Optional; nil discards the messages, which is only ever right
	// for a caller that has nowhere to put them.
	Warnf func(string, ...any)

	// WholeFile says the caller needs the entire chosen file on disk — the
	// download path — rather than only the part that plays. It is what makes a
	// free-space shortfall fatal instead of advisory.
	WholeFile bool
}

// New starts a torrent client and a loopback HTTP server.
func New(opts Options) (*Server, error) {
	dataDir := opts.DataDir
	// A 32-bit build cannot map a 2 GB file, and the failure surfaces deep in
	// the library as "mapping file: invalid argument" after the download has
	// apparently started — worth catching up front with the two things that
	// actually work, both verified.
	if is32Bit && os.Getenv(fileIoEnv) != classicIo {
		return nil, fmt.Errorf(
			"torrent streaming needs a 64-bit build: this one is 32-bit and cannot memory-map a multi-gigabyte file.\n"+
				"Either rebuild with GOARCH=amd64, or re-run with %s=%s", fileIoEnv, classicIo)
	}
	data, err := newDataDir(dataDir)
	if err != nil {
		return nil, fmt.Errorf("creating torrent data directory: %w", err)
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = data.path
	cfg.DefaultStorage = storage.NewFile(data.path)
	// Seeding is what turns watching into distributing. Leave it off by default;
	// the swarm still sees this peer while it leeches, so this is not anonymity,
	// only a smaller footprint.
	cfg.Seed = false
	cfg.NoUpload = true

	client, err := torrent.NewClient(cfg)
	if err != nil {
		data.remove()
		return nil, fmt.Errorf("starting torrent client: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		data.remove()
		return nil, err
	}

	warnf := opts.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	s := &Server{
		client:    client,
		ln:        ln,
		base:      fmt.Sprintf("http://%s", ln.Addr().String()),
		data:      data,
		warnf:     warnf,
		wholeFile: opts.WholeFile,
		entries:   make(map[string]*serveEntry),
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
	// The size is only knowable here: New has nothing but a directory name, and
	// the file list does not exist until GotInfo above. The space verdict is
	// acted on before the priorities below, so a refusal happens before a
	// single piece is wanted.
	idx, verdict, err := s.planServe(infos)
	if err != nil {
		return "", err
	}
	if verdict.refuse {
		return "", fmt.Errorf("%s", verdict.msg)
	}
	if verdict.msg != "" {
		s.warnf("%s", verdict.msg)
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

// planServe chooses the file to serve and decides what to say about free space.
//
// It is split out of Serve because Serve needs a live swarm and this does not,
// and the two guarantees worth testing are both decided here.
//
// The first is which length is measured. It is the chosen file's, never the
// torrent's: Serve sets every other file to PiecePriorityNone, so the extras in
// a season pack are never fetched and must not be allowed to refuse an episode
// that fits on its own.
//
// The second is which filesystem is measured. It stats s.data.path rather than
// $HOME because those are not the same volume in two of the three cases
// newDataDir resolves: a configured torrent_dir may be anywhere, and the
// os.MkdirTemp fallback lands wherever os.TempDir() points. The run directory
// also already exists by now, so a failure here is a real statfs failure rather
// than a missing path.
//
// A failed stat says nothing at all. Guessing in either direction is worse than
// silence: a wrong refusal blocks a torrent that would have played, and a wrong
// reassurance is the mid-playback surprise this check exists to replace.
func (s *Server) planServe(infos []fileInfo) (int, spaceVerdict, error) {
	idx, err := pickVideo(infos)
	if err != nil {
		return 0, spaceVerdict{}, err
	}
	avail, err := diskFree(s.data.path)
	if err != nil {
		return idx, spaceVerdict{}, nil
	}
	return idx, spaceAdvice(path.Base(infos[idx].path), infos[idx].length, avail, s.wholeFile), nil
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

// Close stops the HTTP server and the torrent client, and removes this run's
// data directory.
//
// The payload is deliberately not kept for a later resume. lobster persists no
// torrent state, so nothing could resume it; what a kept directory would
// actually do is accumulate tens of gigabytes per film on the user's home
// volume with nothing ever deleting it — trading the sudden failure this
// replaces for a slow one.
func (s *Server) Close() error {
	if s.srv != nil {
		_ = s.srv.Close()
	}
	if s.client != nil {
		s.client.Close()
	}
	s.data.remove()
	return nil
}

// DataDir is where this run's pieces land. The caller shows it: the default is
// chosen rather than configured, and when no directory under $HOME is usable
// it degrades to a temp directory, which is worth seeing rather than guessing.
func (s *Server) DataDir() string { return s.data.path }

// IsMagnet reports whether a stream URL is a magnet rather than an HTTP stream.
func IsMagnet(u string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), "magnet:")
}
