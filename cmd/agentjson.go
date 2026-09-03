package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// agentSchema versions the machine-readable contract. A skill written against a
// different lobster can detect the mismatch instead of silently misparsing.
const agentSchema = 1

// Exit codes for the agent-facing commands. 2 and 3 are deliberately distinct:
// "no such title" and "the title exists but every source is down" call for
// completely different advice, and on this repo the latter is the common one.
const (
	exitNoResults         = 2
	exitProvidersFailed   = 3
	exitPlayerUnavailable = 4
)

// agentOut is where JSON payloads go. A package var so tests can capture them
// without touching the real stdout.
var agentOut io.Writer = os.Stdout

// exitError carries a process exit code up to Execute. The JSON envelope has
// already been written by the time this is returned, so Execute must not print
// it again.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// emitJSON writes one payload to stdout with the schema marker attached.
func emitJSON(payload map[string]any) error {
	out := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		out[k] = v
	}
	// Set after the copy so a caller-supplied "schema" key can never win;
	// the envelope's schema marker must always be authoritative.
	out["schema"] = agentSchema
	enc := json.NewEncoder(agentOut)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// emitErr writes the error envelope and returns an *exitError carrying the
// process exit code.
func emitErr(code string, exit int, format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	_ = emitJSON(map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
	return &exitError{code: exit, err: fmt.Errorf("%s: %s", code, msg)}
}
