package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// lastAgentBuf is the stdout of the most recent runAgentCmd / runAgentCmdErr
// invocation in the current test. assertErrCode reads it rather than taking
// it as a parameter, to match the brief's call shape
// (assertErrCode(t, "not_configured") with no buffer or error argument).
var lastAgentBuf *bytes.Buffer

// runAgentInvocation drives cmd through the real cobra tree exactly as a user
// (or agent) invoking the binary would: rootCmd.Execute() with cmd's name
// prepended to args. This is deliberate rather than calling cmd.Execute()
// directly — cobra's ExecuteC() always redirects to c.Root().ExecuteC()
// regardless of which command Execute is called on (see
// spf13/cobra command.go: "Regardless of what command execute is called on,
// run on Root only"), so a subcommand's own SetArgs is silently ignored and
// the real root falls back to os.Args. Only a full root invocation exercises
// flag parsing, cobra's Args validation (cobra.NoArgs here), and
// PersistentPreRunE the way the real binary does.
//
// It reuses runCLI (cmd/agenterr_test.go), which already captures agentOut
// via captureAgentOut, isolates HOME/XDG so config.Load never touches a real
// config file, and snapshots+restores flag state on rootCmd and every agent
// command's own flags (including channelsCmd's category/search/limit,
// appended there for this task) so nothing leaks between tests.
func runAgentInvocation(t *testing.T, cmd *cobra.Command, args ...string) cliRun {
	t.Helper()
	full := append([]string{cmd.Name()}, args...)
	r := runCLI(t, full...)
	lastAgentBuf = r.stdout
	return r
}

// runAgentCmd runs cmd and returns its decoded JSON envelope, failing the
// test if the command errored or did not print valid JSON.
func runAgentCmd(t *testing.T, cmd *cobra.Command, args ...string) map[string]any {
	t.Helper()
	r := runAgentInvocation(t, cmd, args...)
	if r.err != nil {
		t.Fatalf("%s %v: %v (stdout=%s stderr=%s)", cmd.Name(), args, r.err, r.stdout.String(), r.stderr.String())
	}
	var got map[string]any
	if jerr := json.Unmarshal(r.stdout.Bytes(), &got); jerr != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", jerr, r.stdout.String())
	}
	return got
}

// runAgentCmdErr runs cmd and returns the error it failed with, failing the
// test if it unexpectedly succeeded.
func runAgentCmdErr(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	r := runAgentInvocation(t, cmd, args...)
	if r.err == nil {
		t.Fatalf("%s %v: succeeded, want an error (stdout=%s)", cmd.Name(), args, r.stdout.String())
	}
	return r.err
}

// assertExit asserts err is an *exitError carrying want.
func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	var ee *exitError
	if !errors.As(err, &ee) {
		t.Fatalf("error = %T (%v), want *exitError", err, err)
	}
	if ee.code != want {
		t.Fatalf("exit code = %d, want %d", ee.code, want)
	}
}

// assertErrCode asserts the last captured envelope's error.code equals want.
func assertErrCode(t *testing.T, want string) {
	t.Helper()
	if lastAgentBuf == nil {
		t.Fatal("assertErrCode called before any runAgentCmd/runAgentCmdErr invocation")
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(lastAgentBuf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, lastAgentBuf.String())
	}
	if got.Error.Code != want {
		t.Fatalf("error.code = %q, want %q (envelope=%s)", got.Error.Code, want, lastAgentBuf.String())
	}
}

func withLiveFixture(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.m3u")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	oldSources, oldTV := agentLiveSources, agentLiveTV
	agentLiveSources = func() []string { return []string{path} }
	t.Cleanup(func() { agentLiveSources, agentLiveTV = oldSources, oldTV })
}

const liveFixtureBody = "#EXTM3U\n" +
	"#EXTINF:-1 tvg-id=\"bbc1.uk\" group-title=\"News\",BBC One\nhttp://example.invalid/1.m3u8\n" +
	"#EXTINF:-1 group-title=\"Sports\",Sky Sports\nhttp://example.invalid/2.m3u8\n" +
	"#EXTINF:-1 group-title=\"News;Sports\",Euronews\nhttp://example.invalid/3.m3u8\n"

func TestChannelsListsCategoriesWithCounts(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd)
	cats := out["categories"].([]any)
	if len(cats) != 2 {
		t.Fatalf("categories = %v, want News and Sports", cats)
	}
	// Euronews is in both groups, so counts sum above the channel total.
	// That is intended: a count answers "how many rows --category X returns".
	byName := map[string]float64{}
	for _, c := range cats {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m["channels"].(float64)
	}
	if byName["News"] != 2 || byName["Sports"] != 2 {
		t.Fatalf("counts = %v, want News:2 Sports:2", byName)
	}
}

func TestChannelsCategoryIsCaseInsensitive(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	// group-title is raw upstream text, so "news" and "News" are distinct
	// buckets there. find already folds case on --type; these must agree.
	out := runAgentCmd(t, channelsCmd, "--category", "news")
	rows := out["channels"].([]any)
	if len(rows) != 2 {
		t.Fatalf("--category news returned %d rows, want 2", len(rows))
	}
	if got := rows[0].(map[string]any)["category"].(string); got != "News" {
		t.Errorf("category echoed as %q, want the playlist's canonical %q", got, "News")
	}
}

func TestChannelsSearchAndCategoryIntersect(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "Sports", "--search", "euro")
	rows := out["channels"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["name"].(string) != "Euronews" {
		t.Fatalf("intersection = %v, want only Euronews", rows)
	}
}

func TestChannelsLimitCaps(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--search", "", "--limit", "1")
	if rows := out["channels"].([]any); len(rows) != 1 {
		t.Fatalf("--limit 1 returned %d rows", len(rows))
	}
}

func TestChannelsEmitsRefNotID(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "Sports")
	row := out["channels"].([]any)[0].(map[string]any)
	if _, hasID := row["id"]; hasID {
		t.Error("channels must not print provider IDs; the ref is the only handle")
	}
	ref, ok := row["ref"].(string)
	if !ok || ref == "" {
		t.Fatal("row carries no ref")
	}
	got, err := decodeRef(ref)
	if err != nil {
		t.Fatalf("emitted ref does not decode: %v", err)
	}
	if got.Type != liveRefType {
		t.Errorf("ref type = %q, want %q", got.Type, liveRefType)
	}
}

func TestChannelsUnknownCategoryIsNoResults(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	err := runAgentCmdErr(t, channelsCmd, "--category", "Cooking")
	assertExit(t, err, exitNoResults)
}

func TestChannelsNoSourcesIsNotConfigured(t *testing.T) {
	old := agentLiveSources
	agentLiveSources = func() []string { return nil }
	t.Cleanup(func() { agentLiveSources = old })

	err := runAgentCmdErr(t, channelsCmd)
	// Exit 1, not 2. "You have no playlists configured" is a config fix,
	// not a query to rephrase — the distinction find could not express.
	assertExit(t, err, exitUsage)
	assertErrCode(t, "not_configured")
}

func TestChannelsRejectsPositionalArgs(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	// markAgentCommand installs ArbitraryArgs when a command sets none, so
	// without cobra.NoArgs `lobster channels Sports` would silently ignore
	// its argument and list categories instead.
	err := runAgentCmdErr(t, channelsCmd, "Sports")
	assertExit(t, err, exitUsage)
}
