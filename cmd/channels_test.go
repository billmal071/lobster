package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"lobster/internal/provider"
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
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
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
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
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
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
	rows := out["channels"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["name"].(string) != "Euronews" {
		t.Fatalf("intersection = %v, want only Euronews", rows)
	}
}

func TestChannelsLimitCaps(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--search", "", "--limit", "1")
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
	if rows := out["channels"].([]any); len(rows) != 1 {
		t.Fatalf("--limit 1 returned %d rows", len(rows))
	}
}

func TestChannelsEmitsRefNotID(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "Sports")
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
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

// A category value of the empty string is a legitimate --category call
// ("list channels with no group at all" is out of scope here, but the point
// is the caller explicitly asked for the row-listing view), and must not be
// mistaken for "the flag was never given". Checking flagChannelsCategory !=
// "" instead of cmd.Flags().Changed("category") conflated the two: --search
// already uses Changed, so this asserts --category is symmetric with it.
func TestChannelsEmptyCategoryFlagStillLists(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	out := runAgentCmd(t, channelsCmd, "--category", "")
	if _, isCategories := out["categories"]; isCategories {
		t.Fatalf("--category \"\" (explicitly given) produced the categories view: %v", out)
	}
	rows, ok := out["channels"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("channels = %v, want all 3 rows (an explicit but empty --category matches everything, like --search \"\")", out["channels"])
	}
}

// withPartialLiveFixture returns one source that loads a fixed set of
// channels and one that never loads (a path LiveTV.fetch's os.ReadFile
// branch cannot open, since it names no file — this is a local-path failure,
// not a network call). It returns the failing source's path so tests can
// assert it by name.
func withPartialLiveFixture(t *testing.T, body string) (badSource string) {
	t.Helper()
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.m3u")
	if err := os.WriteFile(goodPath, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	badPath := filepath.Join(dir, "missing.m3u") // deliberately never created
	old := agentLiveSources
	agentLiveSources = func() []string { return []string{goodPath, badPath} }
	t.Cleanup(func() { agentLiveSources = old })
	return badPath
}

// One playlist down must not make channels that live only in the surviving
// playlist vanish, nor make the caller believe the catalog is simply smaller
// than it is. A channel query that still turns up rows must say the listing
// is incomplete, not stay silent about it.
func TestChannelsPartialFailureSurfacesButStillLists(t *testing.T) {
	badSource := withPartialLiveFixture(t, liveFixtureBody)

	out := runAgentCmd(t, channelsCmd, "--category", "Sports")
	if out["schema"] != float64(1) {
		t.Fatalf("schema = %v, want 1", out["schema"])
	}
	rows, ok := out["channels"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("channels = %v, want the 2 Sports rows despite one failed source", out["channels"])
	}
	failed, ok := out["failed_sources"].([]any)
	if !ok || len(failed) != 1 || failed[0].(string) != badSource {
		t.Fatalf("failed_sources = %v, want [%q]", out["failed_sources"], badSource)
	}
}

// The categories view (no --category/--search) must carry the same honesty:
// a category that only existed on the dead playlist must not silently read
// as "there is no such category".
func TestChannelsPartialFailureSurfacesOnCategoriesView(t *testing.T) {
	badSource := withPartialLiveFixture(t, liveFixtureBody)

	out := runAgentCmd(t, channelsCmd)
	cats, ok := out["categories"].([]any)
	if !ok || len(cats) != 2 {
		t.Fatalf("categories = %v, want News and Sports despite one failed source", out["categories"])
	}
	failed, ok := out["failed_sources"].([]any)
	if !ok || len(failed) != 1 || failed[0].(string) != badSource {
		t.Fatalf("failed_sources = %v, want [%q]", out["failed_sources"], badSource)
	}
}

// The dangerous case: a query that matches nothing because the only channels
// that would have matched lived on the failed playlist. Without consulting
// FailedSources this reads as "no such channel" (exit 2, give up) when the
// truth is "a playlist is down, try again" (exit 3). An empty good source
// plus one failed source isolates this from "genuinely nothing configured
// matches" (TestChannelsUnknownCategoryIsNoResults, no failed source there).
func TestChannelsEmptyResultWithFailedSourceIsProvidersFailed(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty.m3u")
	if err := os.WriteFile(emptyPath, []byte("#EXTM3U\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	badPath := filepath.Join(dir, "missing.m3u")
	old := agentLiveSources
	agentLiveSources = func() []string { return []string{emptyPath, badPath} }
	t.Cleanup(func() { agentLiveSources = old })

	err := runAgentCmdErr(t, channelsCmd)
	assertExit(t, err, exitProvidersFailed)
	assertErrCode(t, "providers_failed")
}

// Every source failing to load is the state LoadContext itself already
// detects and errors on; pinned here as a regression test alongside the
// partial-failure cases above so the two are not confused with each other.
func TestChannelsAllSourcesFailedIsProvidersFailed(t *testing.T) {
	dir := t.TempDir()
	bad1 := filepath.Join(dir, "missing1.m3u")
	bad2 := filepath.Join(dir, "missing2.m3u")
	old := agentLiveSources
	agentLiveSources = func() []string { return []string{bad1, bad2} }
	t.Cleanup(func() { agentLiveSources = old })

	err := runAgentCmdErr(t, channelsCmd)
	assertExit(t, err, exitProvidersFailed)
	assertErrCode(t, "providers_failed")
}

// --- Round 2: failed sources must not leak Xtream credentials ---
//
// internal/config/config.go builds an Xtream source URL as
// "<server>/get.php?username=<u>&password=<p>&type=m3u_plus&output=m3u8", so
// FailedSources() can return a string carrying the subscriber's live-TV
// password verbatim. Round 1 made that string flow into both the error
// message and the "failed_sources" envelope field; this section pins that it
// never reaches either un-redacted.

// credentialSource is shaped exactly like the Xtream URL config.go builds:
// scheme, host, path, and a query string carrying username+password.
const credentialSource = "https://iptv.example.invalid/get.php?username=alice&password=SUPERSECRET12345&type=m3u_plus"

// credentialSourceSecret is the exact substring that must never appear
// anywhere in emitted output. Checked as a literal string, not "looks
// redacted" — a test that only asserted the host survived would still pass
// with the password riding along in an adjacent field.
const credentialSourceSecret = "SUPERSECRET12345"

func TestDisplaySourceStripsCredentialsFromURL(t *testing.T) {
	got := displaySource(credentialSource)
	if strings.Contains(got, credentialSourceSecret) {
		t.Fatalf("displaySource leaked the password: %q", got)
	}
	if strings.Contains(got, "alice") {
		t.Fatalf("displaySource leaked the username: %q", got)
	}
	if got != "https://iptv.example.invalid/get.php" {
		t.Fatalf("displaySource(%q) = %q, want scheme://host/path with userinfo and the whole query dropped", credentialSource, got)
	}
}

// Local paths carry no query string and are the natural identifier for
// "which playlist is down" — deliberately left unchanged, not an oversight.
func TestDisplaySourceLeavesLocalPathsUnchanged(t *testing.T) {
	path := "/home/user/.config/lobster/playlists/mine.m3u"
	if got := displaySource(path); got != path {
		t.Fatalf("displaySource(%q) = %q, want unchanged", path, got)
	}
}

// loadedLiveTV loads sources through the real (non-network) fixture path and
// returns the provider, for tests that call emitChannelRows/
// emitChannelCategories directly with a fabricated failed-sources list —
// this is what lets the credential case be exercised without any actual
// network attempt: the failure is supplied by the test, not produced by a
// real unreachable http(s) source.
func loadedLiveTV(t *testing.T) *provider.LiveTV {
	t.Helper()
	p := agentLiveTV(agentLiveSources())
	if err := p.LoadContext(context.Background()); err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	return p
}

// The empty-result path: a category that matches nothing, so the credential
// source drives the emitErr message itself.
func TestChannelsRowsErrorMessageOmitsCredentials(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	buf := captureAgentOut(t)
	p := loadedLiveTV(t)

	prevCat := flagChannelsCategory
	flagChannelsCategory = "NoSuchCategory"
	t.Cleanup(func() { flagChannelsCategory = prevCat })

	err := emitChannelRows(p, []string{credentialSource})
	if err == nil {
		t.Fatal("emitChannelRows succeeded, want providers_failed (no channel matches NoSuchCategory)")
	}
	if strings.Contains(err.Error(), credentialSourceSecret) {
		t.Fatalf("error message leaked the password: %q", err.Error())
	}
	if strings.Contains(buf.String(), credentialSourceSecret) {
		t.Fatalf("emitted envelope leaked the password: %s", buf.String())
	}
}

// The non-empty-result path: rows still print, and the envelope's
// failed_sources entry must be the sanitized form, not the raw source.
func TestChannelsRowsEnvelopeOmitsCredentials(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	buf := captureAgentOut(t)
	p := loadedLiveTV(t)

	prevCat := flagChannelsCategory
	flagChannelsCategory = "Sports"
	t.Cleanup(func() { flagChannelsCategory = prevCat })

	if err := emitChannelRows(p, []string{credentialSource}); err != nil {
		t.Fatalf("emitChannelRows: %v", err)
	}
	if strings.Contains(buf.String(), credentialSourceSecret) {
		t.Fatalf("emitted envelope leaked the password: %s", buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	failed, ok := got["failed_sources"].([]any)
	if !ok || len(failed) != 1 {
		t.Fatalf("failed_sources = %v, want one sanitized entry", got["failed_sources"])
	}
	if failed[0].(string) != "https://iptv.example.invalid/get.php" {
		t.Fatalf("failed_sources[0] = %q, want the sanitized scheme://host/path", failed[0])
	}
}

// The categories view must apply the same sanitization; it shares
// FailedSources with the rows view but is a separate code path.
func TestChannelsCategoriesEnvelopeOmitsCredentials(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	buf := captureAgentOut(t)
	p := loadedLiveTV(t)

	if err := emitChannelCategories(p, []string{credentialSource}); err != nil {
		t.Fatalf("emitChannelCategories: %v", err)
	}
	if strings.Contains(buf.String(), credentialSourceSecret) {
		t.Fatalf("emitted envelope leaked the password: %s", buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad JSON: %v (%q)", err, buf.String())
	}
	failed, ok := got["failed_sources"].([]any)
	if !ok || len(failed) != 1 || failed[0].(string) != "https://iptv.example.invalid/get.php" {
		t.Fatalf("failed_sources = %v, want [%q]", got["failed_sources"], "https://iptv.example.invalid/get.php")
	}
}

// TestChannelsRefOmitsCredentialsInDecodedPayload asserts on the *decoded*
// ref, not the envelope text: encodeRef base64-encodes, it does not redact,
// so a strings.Contains(output, secret) check cannot see through it. A
// channel loaded from a credentialed Xtream source must not carry the raw
// source into its ref's Source field on the success path (every prior
// credential test here only injects the credentialed string as a
// FailedSources() entry, so none of them ever mint a ref from a credentialed
// Channel.Source).
func TestChannelsRefOmitsCredentialsInDecodedPayload(t *testing.T) {
	ch := provider.Channel{
		ID:     "chan1",
		Name:   "Alpha",
		Source: credentialSource,
	}
	ref, err := liveChannelRef(ch)
	if err != nil {
		t.Fatalf("liveChannelRef: %v", err)
	}
	decoded, err := decodeRef(ref)
	if err != nil {
		t.Fatalf("ref does not decode: %v", err)
	}
	if strings.Contains(decoded.Source, credentialSourceSecret) {
		t.Fatalf("decoded ref Source leaked the password: %q", decoded.Source)
	}
	// Belt and braces: check the whole decoded struct, not only the field we
	// expect to carry it, in case a future field starts copying ch.Source.
	blob, _ := json.Marshal(decoded)
	if strings.Contains(string(blob), credentialSourceSecret) {
		t.Fatalf("decoded ref leaked the password somewhere: %s", blob)
	}
}
