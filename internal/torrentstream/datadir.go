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

// dataStaleAfter is how long a data directory may go without a sign of life
// before a later run sweeps it.
//
// The sign of life is the ownership marker, refreshed every dataTouchEvery while
// this run is alive — not the directory's own mtime, which does not move while
// the client writes into files it already allocated and so says nothing about
// whether the run is over. 24 hours is then simply how long after a kill the
// payload is left alone, and the only way a live run is swept is if it stops
// being scheduled for a whole day.
const dataStaleAfter = 24 * time.Hour

// dataTouchEvery is how often a live run refreshes its directory's marker. A
// quarter of the stale window leaves three missed refreshes of slack before a
// second lobster could consider this run abandoned, at a cost of one timestamp
// update per run every six hours. It is a var so a test can drive the loop
// rather than wait out a real interval.
var dataTouchEvery = dataStaleAfter / 4

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
func newDataDir(configured string) (runDir, error) {
	if configured == "" {
		// userdir falls back to os.MkdirTemp when no base under $HOME is
		// usable (a container with no writable home, say). That is the
		// filesystem this function exists to avoid, but refusing to play at
		// all is worse — so it is accepted and surfaced instead: the caller
		// prints the path it is about to fill.
		dir, _, err := userdir.Make(dataPrefix, dataStaleAfter, nil)
		if err != nil {
			return runDir{}, err
		}
		return runDir{path: dir, ownParent: true, stopAlive: userdir.Keepalive(dir, dataTouchEvery)}, nil
	}
	if err := os.MkdirAll(configured, 0o700); err != nil {
		return runDir{}, err
	}
	userdir.PruneStale(configured, dataPrefix, dataStaleAfter)
	dir, err := os.MkdirTemp(configured, dataPrefix+"-")
	if err != nil {
		return runDir{}, err
	}
	// Without the marker a later run will not sweep this directory, so a run
	// killed before Close would leave its payload in the user's directory
	// forever. Failing here is the honest answer: a directory we just created
	// and cannot write a zero-byte file into will not hold a download either.
	if err := userdir.MarkOwned(dir); err != nil {
		_ = os.RemoveAll(dir)
		return runDir{}, err
	}
	return runDir{path: dir, stopAlive: userdir.Keepalive(dir, dataTouchEvery)}, nil
}

// runDir is where one run's pieces land, together with how much of the path
// above it the run is entitled to delete.
type runDir struct {
	path string

	// ownParent records that lobster chose the parent directory as well as the
	// run directory — the default, where userdir.Make creates
	// <base>/.lobster/<run>. Only then may cleanup tidy the parent away once
	// it empties.
	//
	// It is false for a configured torrent_dir, and that is not a detail:
	// userdir.Remove decides whether to remove the parent by comparing its
	// *name* to userdir.Parent, so a user who points torrent_dir at a
	// directory of their own called ".lobster" would otherwise have it deleted
	// — with its permissions — the first time a run finished. A configured
	// directory is never lobster's to remove, whatever it is called.
	ownParent bool

	// stopAlive ends the goroutine that keeps refreshing the directory's
	// liveness marker. A run that is killed without reaching it stops
	// refreshing by itself, which is what keeps growth bounded — see
	// userdir.Keepalive.
	stopAlive func()
}

// remove deletes the run's data directory, and lobster's own shared parent with
// it when that parent is now empty.
func (d runDir) remove() {
	if d.stopAlive != nil {
		d.stopAlive()
	}
	if d.path == "" {
		return
	}
	if d.ownParent {
		userdir.Remove(d.path)
		return
	}
	_ = os.RemoveAll(d.path)
}
