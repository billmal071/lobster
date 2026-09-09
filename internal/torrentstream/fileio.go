package torrentstream

import (
	"fmt"
	"os"
)

// fileIoEnv selects the storage layer's file backend. The torrent library reads
// it in its own package init(), so by the time any of our code runs the choice
// is already made and os.Setenv is too late — the only way to change it is to
// start the process again with the variable already set.
const fileIoEnv = "TORRENT_STORAGE_DEFAULT_FILE_IO"

// classicIo is the backend that does not memory-map.
const classicIo = "classic"

// Why this exists at all: with the variable unset the library defaults to its
// mmap backend, where openForWrite can truncate a file while another handle
// still holds a live mapping of it. Touching the truncated tail of a mapping is
// a SIGBUS, which is a signal rather than a Go error — no recover, no
// stack trace worth reading, the whole process dies mid-playback. The window is
// narrow (it needs a shrinking truncate racing a read), but the cost when it
// opens is total, and the classic backend simply does not have it.
//
// Neither obvious fix applies. The library exposes no programmatic choice:
// defaultFileIo is an unexported package variable and fileIo an unexported
// interface, and every exported constructor — NewFile, NewFileOpts and the
// deprecated NewFileWith* family — funnels through it. And there is no fixed
// release to move to; v1.61.0 is the newest published version.

// ioPlan is what ensureClassicFileIo should do about the backend. Keeping the
// decision separate from the act is what makes it testable: reproducing an
// actual truncate/mmap race is inherently flaky, but "given this environment,
// do we re-exec, and with what?" is exact.
type ioPlan struct {
	// exec re-executes this process with value set for fileIoEnv.
	exec bool
	// value is the backend to select when exec is set.
	value string
	// warn, when non-empty, is a message to show instead of re-executing.
	warn string
}

// planFileIo decides how to reach the classic backend.
//
// willStream says the run may open a magnet. set and current describe
// fileIoEnv as the process was started with. canExec says the platform can
// replace its own process image.
//
// An explicit setting is always left alone, including "mmap": choosing the
// faster backend and accepting the race is the user's call to make, and
// silently overriding it would make their setting a lie.
func planFileIo(willStream, set bool, current string, canExec bool) ioPlan {
	if !willStream || set {
		return ioPlan{}
	}
	if !canExec {
		return ioPlan{warn: fmt.Sprintf(
			"torrent streaming is using the memory-mapped storage backend, where a truncate racing a read can kill lobster with SIGBUS.\n"+
				"To use the slower backend that cannot, start lobster with %s=%s set in the environment.", fileIoEnv, classicIo)}
	}
	return ioPlan{exec: true, value: classicIo}
}

// ensureClassicFileIo puts the process on the classic storage backend when a
// run may stream a torrent, by starting it again with fileIoEnv set.
//
// On success this call does not return: the process image is replaced, keeping
// the same pid, file descriptors, controlling terminal and exit status, so
// nothing supervises anything and there is no second process to reason about.
// It must therefore run before anything the user would not want repeated —
// which is why the caller is config loading rather than the playback path that
// discovers the magnet.
//
// A failure to re-exec is reported and not fatal. Refusing to run at all would
// trade a narrow race for a certain outage.
func ensureClassicFileIo(willStream bool, warnf func(string, ...any)) {
	current, set := os.LookupEnv(fileIoEnv)
	plan := planFileIo(willStream, set, current, canExec)
	switch {
	case plan.warn != "":
		warnf("%s", plan.warn)
	case plan.exec:
		if err := execSelf(fileIoEnv + "=" + plan.value); err != nil {
			warnf("could not switch to the %s storage backend (%v); continuing on the memory-mapped one, "+
				"where a truncate racing a read can kill lobster with SIGBUS", plan.value, err)
		}
	}
}

// EnsureSafeStorage selects a torrent storage backend that cannot SIGBUS, for a
// run that may open a magnet. It is a no-op for a run that cannot, and for one
// whose backend the user has already chosen.
//
// willStream should be true whenever the YTS provider is reachable — as the
// primary source, as an enabled fallback, or by the caller's own routing —
// since it is the only provider that resolves to a magnet. The caller decides;
// see mayStreamTorrent (cmd/root.go) for what reaches YTS today.
func EnsureSafeStorage(willStream bool, warnf func(string, ...any)) {
	ensureClassicFileIo(willStream, warnf)
}

// WarnLateStorageRisk reports the exposure for a run that only turns out to
// need a torrent after EnsureSafeStorage has already answered — a ref carrying
// its own base, which the playback command copies into the config inside RunE,
// long after PersistentPreRunE picked the backend.
//
// It warns rather than re-execs on every platform, not just where canExec is
// false. A re-exec replays argv, so it is only safe before anything the user
// would not want repeated; by the time a ref's base has been applied the
// process is committed to this invocation. Warning is what is left.
//
// An explicit fileIoEnv is respected exactly as it is elsewhere: a user who
// chose the backend does not need to be told about the one they chose.
func WarnLateStorageRisk(willStream bool, warnf func(string, ...any)) {
	_, set := os.LookupEnv(fileIoEnv)
	if plan := planFileIo(willStream, set, "", false); plan.warn != "" {
		warnf("%s", plan.warn)
	}
}
