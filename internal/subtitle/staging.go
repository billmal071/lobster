package subtitle

import (
	"time"

	"lobster/internal/userdir"
)

// staleAfter is how long an abandoned staging directory is kept before a later
// run sweeps it.
//
// A staging directory's mtime is set when its subtitle files are written, at
// the start of playback, and reading those files afterwards never advances it.
// So a session that runs longer than staleAfter can have its own live staging
// directory swept by a second lobster process starting in the meantime. That
// window is left open deliberately rather than guarded with a lock: staged
// files are handed to the player only as launch arguments, and nothing reopens
// one mid-playback. mpv is given every track at launch and then holds no
// descriptor on the files at all — selecting a track whose file has since been
// deleted still renders it, because the file was read in full at load time —
// and VLC is given a single file at launch. Losing the directory under a
// running player is therefore not observable; the process's own Cleanup on a
// pruned directory is a no-op.
const staleAfter = 24 * time.Hour

// stagingPrefix names staging directories within the shared parent.
const stagingPrefix = "subs"

// newStagingDir creates a fresh staging directory a confined player can read,
// falling back to the system temp dir when nothing under $HOME is usable (a
// container with no writable home, say) — an unreadable subtitle is better
// than no playback at all.
func newStagingDir() (string, error) {
	dir, _, err := userdir.Make(stagingPrefix, staleAfter, nil)
	return dir, err
}

// removeStagingDir deletes a staging directory and, if that leaves the shared
// parent empty, the parent too.
func removeStagingDir(dir string) { userdir.Remove(dir) }
