package torrentstream

import (
	"os"
	"time"

	"lobster/internal/userdir"
)

// dataPrefix names a run's data directory inside the shared parent. The prune
// sweep recognises directories by this prefix, so it is also what keeps the
// sweep away from anything else living in a user-chosen directory.
const dataPrefix = "torrent"

// dataStaleAfter is how long an abandoned data directory is kept before a
// later run sweeps it.
//
// A data directory's mtime advances while pieces are written, so a download in
// progress is never a sweep candidate; what stops advancing is a payload that
// has finished downloading while playback continues. 24 hours is therefore
// chosen to sit well beyond the longest plausible single sitting, and the only
// cost of being wrong is that a still-playing file is unlinked underneath a
// player that already holds it open — the bytes stay readable until it closes.
const dataStaleAfter = 24 * time.Hour

// newDataDir creates the directory a run's torrent pieces land in.
//
// configured is the user's torrent_dir, empty for the default. The default
// goes through userdir, which puts it on the home volume
// (~/Videos/.lobster/torrent-<random>) rather than under os.TempDir(). That
// matters twice over: a torrent payload is the largest thing lobster writes —
// a 4K remux runs to tens of gigabytes, enough to fill a root filesystem that
// has room for nothing else — and /tmp is private to a snap-confined process,
// so a path written there is not reachable from outside it.
//
// Either way the result is a fresh per-run subdirectory, not the parent
// itself, and abandoned siblings are swept on the way in. That is what makes
// Close able to delete the payload without reasoning about what else is in
// there, and what stops a run killed before Close from leaving its partial
// download behind forever: unlike /tmp, nothing reclaims a directory under
// $HOME.
func newDataDir(configured string) (string, error) {
	if configured == "" {
		// userdir falls back to os.MkdirTemp when no base under $HOME is
		// usable (a container with no writable home, say). That is the
		// filesystem this function exists to avoid, but refusing to play at
		// all is worse — so it is accepted and surfaced instead: the caller
		// prints the path it is about to fill.
		dir, _, err := userdir.Make(dataPrefix, dataStaleAfter, nil)
		return dir, err
	}
	if err := os.MkdirAll(configured, 0o700); err != nil {
		return "", err
	}
	userdir.PruneStale(configured, dataPrefix, dataStaleAfter)
	return os.MkdirTemp(configured, dataPrefix+"-")
}

// removeDataDir deletes a run's data directory, and the shared parent with it
// when that parent is lobster's own and now empty. A user-chosen torrent_dir
// is never removed.
func removeDataDir(dir string) { userdir.Remove(dir) }
