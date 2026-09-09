package torrentstream

import "errors"

// canExec is false on Windows: there is no execve, and the alternative — a
// child process the parent supervises — would have to forward signals, console
// handles and the exit status, which is a great deal of machinery for a race
// the user can avoid by setting one variable.
const canExec = false

// execSelf never runs on Windows; canExec keeps callers away from it.
func execSelf(string) error {
	return errors.New("no exec on windows")
}
