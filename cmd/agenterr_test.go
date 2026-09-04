package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cliRun is the result of driving the real cobra tree end to end.
type cliRun struct {
	stdout *bytes.Buffer // what an agent parses: the JSON envelope writer
	stderr *bytes.Buffer // what cobra prints for humans
	err    error
}

// rootPersistentFlagNames is every persistent flag an Execute() in these
// tests can mutate; snapshotting them keeps one run from leaking Changed
// bits or values into the next (the commands are package-level singletons).
var rootPersistentFlagNames = []string{
	"download", "language", "audio-language", "no-subs", "provider",
	"quality", "player", "continue", "json", "base", "debug",
}

// runCLI executes the real root command with args, exactly as main() would,
// but with the JSON writer and cobra's error stream captured and with every
// global the run mutates restored afterwards.
//
// Driving Execute() rather than calling the RunE functions directly is the
// whole point: the failures this covers (argument validation, flag parsing,
// PersistentPreRunE) all happen *before* RunE is ever reached, so a test that
// calls findRun/playRun cannot see them at all.
func runCLI(t *testing.T, args ...string) cliRun {
	t.Helper()

	// Keep config.Load away from the developer's real config file.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))

	snapshotFlags(t, rootCmd, rootPersistentFlagNames...)
	snapshotFlags(t, findCmd, "type", "limit")
	snapshotFlags(t, episodesCmd, "ref", "season")
	snapshotFlags(t, playCmd, "ref", "season", "episode", "detach", "supervised")

	prevCfg := cfg
	t.Cleanup(func() { cfg = prevCfg })

	out := captureAgentOut(t)

	var errBuf bytes.Buffer
	rootCmd.SetOut(&errBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	err := rootCmd.Execute()
	return cliRun{stdout: out, stderr: &errBuf, err: err}
}

// snapshotFlags records the named flags' values and Changed bits, restoring
// them when the test ends. The *pflag.Flag values are obtained through cobra's
// accessors and only ever held in inferred-type variables, so this file needs
// no direct pflag dependency.
func snapshotFlags(t *testing.T, c *cobra.Command, names ...string) {
	t.Helper()
	for _, name := range names {
		f := c.PersistentFlags().Lookup(name)
		if f == nil {
			f = c.Flags().Lookup(name)
		}
		if f == nil {
			t.Fatalf("flag %q not found on %q; the snapshot list is stale", name, c.Name())
		}
		changed, val := f.Changed, f.Value.String()
		t.Cleanup(func() {
			_ = f.Value.Set(val)
			f.Changed = changed
		})
	}
}

// wantEnvelope asserts stdout carries a parseable error envelope and returns
// its code.
func wantEnvelope(t *testing.T, r cliRun) string {
	t.Helper()
	var got struct {
		Schema int `json:"schema"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if r.stdout.Len() == 0 {
		t.Fatalf("stdout was empty; an agent parsing it unconditionally gets nothing (stderr=%q, err=%v)", r.stderr.String(), r.err)
	}
	if err := json.Unmarshal(r.stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v (%q)", err, r.stdout.String())
	}
	if got.Schema != 1 {
		t.Fatalf("schema = %d, want 1", got.Schema)
	}
	if got.Error.Code == "" {
		t.Fatalf("error.code is empty in %q", r.stdout.String())
	}
	if got.Error.Message == "" {
		t.Fatalf("error.message is empty in %q", r.stdout.String())
	}
	return got.Error.Code
}

// wantExitCode asserts the run failed with an *exitError carrying want.
func wantExitCode(t *testing.T, r cliRun, want int) {
	t.Helper()
	var ee *exitError
	if !errors.As(r.err, &ee) {
		t.Fatalf("Execute returned %T (%v), want *exitError so Execute() can pick the exit code", r.err, r.err)
	}
	if ee.code != want {
		t.Fatalf("exit code = %d, want %d", ee.code, want)
	}
}

// Class 1: Args validation. "lobster find" with no query fails cobra's
// MinimumNArgs before RunE runs, and SilenceErrors means nothing is printed
// at all unless the command converts it into an envelope itself.
func TestAgentCommandArgsErrorIsJSON(t *testing.T) {
	r := runCLI(t, "find")
	if code := wantEnvelope(t, r); code != "usage" {
		t.Fatalf("error.code = %q, want usage", code)
	}
	wantExitCode(t, r, exitUsage)
}

// Class 2: flag parsing. An unknown flag is rejected inside ParseFlags,
// before Args validation and before PersistentPreRunE.
func TestAgentCommandFlagErrorIsJSON(t *testing.T) {
	r := runCLI(t, "find", "x", "--bogus")
	if code := wantEnvelope(t, r); code != "usage" {
		t.Fatalf("error.code = %q, want usage", code)
	}
	wantExitCode(t, r, exitUsage)
}

// Class 3: PersistentPreRunE. loadConfig rejects an invalid --quality after
// flags parse and args validate, on the way to RunE.
func TestAgentCommandConfigErrorIsJSON(t *testing.T) {
	r := runCLI(t, "find", "x", "--quality", "9999")
	if code := wantEnvelope(t, r); code != "config_invalid" {
		t.Fatalf("error.code = %q, want config_invalid", code)
	}
	wantExitCode(t, r, exitUsage)
}

// The other two agent commands must behave the same way; the envelope is a
// property of the agent surface, not of one command.
func TestAgentCommandsAllEnvelopeUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"episodes-args", []string{"episodes", "stray-positional"}},
		{"episodes-flag", []string{"episodes", "--bogus"}},
		{"play-args", []string{"play", "stray-positional"}},
		{"play-flag", []string{"play", "--bogus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runCLI(t, tc.args...)
			wantEnvelope(t, r)
			wantExitCode(t, r, exitUsage)
		})
	}
}

// Hard constraint: the interactive root command is not an agent surface and
// its human-facing behaviour must not change. A bad --quality must still
// print "Error: ..." on stderr and must not emit a JSON envelope.
func TestRootCommandConfigErrorStaysHumanReadable(t *testing.T) {
	r := runCLI(t, "somequery", "--quality", "9999")

	if r.err == nil {
		t.Fatal("Execute succeeded with an invalid --quality")
	}
	var ee *exitError
	if errors.As(r.err, &ee) {
		t.Fatalf("root command returned an *exitError (%v); it must stay a plain error", r.err)
	}
	if r.stdout.Len() != 0 {
		t.Fatalf("root command wrote a JSON envelope to stdout: %q", r.stdout.String())
	}
	if !strings.Contains(r.stderr.String(), "Error:") {
		t.Fatalf("stderr = %q, want cobra's human-readable \"Error: ...\"", r.stderr.String())
	}
	if !strings.Contains(r.stderr.String(), "invalid configuration") {
		t.Fatalf("stderr = %q, want the invalid-configuration message", r.stderr.String())
	}
}
