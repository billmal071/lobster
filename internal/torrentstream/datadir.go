package torrentstream

import (
	"os"
	"time"

	"lobster/internal/userdir"
)

// dataPrefix names a run's data directory inside the shared parent.
//
// The prefix alone is not what keeps the prune sweep away from the user's own
// files: a configured torrent_dir may be a directory they also use, and
// "torrent-backups" is a name they are entitled to. Ownership is established by
// the marker userdir writes inside every directory lobster creates, and the
// sweep deletes nothing without it.
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
// itself, marked as lobster's, and abandoned siblings that carry that mark are
// swept on the way in. That is what makes
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
	dir, err := os.MkdirTemp(configured, dataPrefix+"-")
	if err != nil {
		return "", err
	}
	// Without the marker a later run will not sweep this directory, so a run
	// killed before Close would leave its payload in the user's directory
	// forever. Failing here is the honest answer: a directory we just created
	// and cannot write a zero-byte file into will not hold a download either.
	if err := userdir.MarkOwned(dir); err != nil {
		userdir.Remove(dir)
		return "", err
	}
	return dir, nil
}

// removeDataDir deletes a run's data directory, and the shared parent with it
// when that parent is lobster's own and now empty. A user-chosen torrent_dir
// is never removed.
func removeDataDir(dir string) { userdir.Remove(dir) }
