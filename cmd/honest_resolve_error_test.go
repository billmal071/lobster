package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
	"lobster/internal/resolver"
)

// deadStreamer is a chain member that has the show and refuses to stream it.
//
// GetSeasons answers nil/nil on purpose: fallbackSeasonHits must find no hit,
// because a hit would make the season-existence gate at cmd/search.go the
// thing under test rather than the resolver. nil/nil is also what the real
// StreamProviders in the chain do (recordingStreamProvider above, and
// *provider.VaPlayer, which has no season index at all).
type deadStreamer struct {
	*stubProvider
	err error
}

func (d *deadStreamer) Watch(string, string, string, string) (*media.Stream, error) {
	return nil, d.err
}

// Two distinct concrete types, because resolver.ProviderName keys every report
// row on the type name (internal/resolver/health.go:24). One type used twice
// would collapse the two chain members into one indistinguishable row, and the
// whole point of the per-provider list is that the rows are separable.
type deadStreamerAlpha struct{ *deadStreamer }
type deadStreamerBeta struct{ *deadStreamer }

// newDeadChain builds a chain whose members all have the show and all fail to
// stream it, each with its own distinguishable reason. "status 404" rather than
// a 5xx: resolver.isTransient (internal/resolver/classify.go:29) retries a
// transient error after a 250ms sleep, so a 5xx would double every probe.
func newDeadChain(title string) (provider.Provider, provider.Provider) {
	hit := []media.SearchResult{{ID: "dead/1", Title: title, Type: media.TV}}
	a := &deadStreamerAlpha{&deadStreamer{
		stubProvider: &stubProvider{results: hit},
		err:          errors.New("status 404 alpha"),
	}}
	b := &deadStreamerBeta{&deadStreamer{
		stubProvider: &stubProvider{results: hit},
		err:          errors.New("status 404 beta"),
	}}
	return a, b
}

// withPlayerAvailable forces the player precondition to pass without depending
// on a binary being installed (the local checkout has /usr/bin/mpv, CI runners
// do not).
func withPlayerAvailable(t *testing.T) {
	t.Helper()
	prev := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "" }
	t.Cleanup(func() { agentPlayerCheck = prev })
}

// errEnvelope is the error half of the agent JSON envelope, with the additive
// per-provider list.
type errEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Providers []struct {
			Provider   string `json:"provider"`
			Stage      string `json:"stage"`
			Error      string `json:"error"`
			DurationMs int64  `json:"duration_ms"`
		} `json:"providers"`
	} `json:"error"`
	Schema int `json:"schema"`
}

func decodeErrEnvelope(t *testing.T, s string) errEnvelope {
	t.Helper()
	var env errEnvelope
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		t.Fatalf("envelope %q is not JSON: %v", s, err)
	}
	return env
}

// A primary that cannot enumerate seasons is a state resolveAndPlay explicitly
// continues past: validateSeasonEpisode logs "deferring to the resolver"
// (cmd/play.go:150) and the unenumerable branch at cmd/search.go:323 hands the
// request to tryFallbackStream. When that resolver then fails, the reported
// cause must be the resolver's — not the season error the code had already
// decided was not fatal.
//
// cmd/search.go:362 returned `fmt.Errorf("getting seasons: %w", err)` and
// dropped fbErr, so a run where every provider in the chain failed to produce
// a stream reported "getting seasons: no seasons found" and sent the reader to
// look at season enumeration. The per-provider reasons the resolver had
// already assembled existed only behind -x.
func TestPlayReportsResolverCauseNotSupersededSeasonError(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	buf := captureAgentOut(t)
	withPlayerAvailable(t)

	a, b := newDeadChain("Some Show")
	withFallbackChain(t, a, b)
	// The exact error the live run reported, so the assertion below is about
	// the real message and not a stand-in for it.
	withStubProvider(t, &stubProvider{seasonsErr: errors.New("no seasons found")})

	withEpisodesFlags(t, tvRef(t, ""), 1)
	prevEpisode := flagEpisode
	flagEpisode = 1
	t.Cleanup(func() { flagEpisode = prevEpisode })

	err := playRun(playCmd, nil)
	if err == nil {
		t.Fatalf("play succeeded with a chain that streams nothing; it emitted %q", buf.String())
	}

	env := decodeErrEnvelope(t, buf.String())

	// "every provider failed to stream" must not masquerade as "no such
	// title": the two call for completely different advice.
	if env.Error.Code != "providers_failed" {
		t.Errorf("error code = %q, want providers_failed", env.Error.Code)
	}
	if got := exitCodeOf(err); got != exitProvidersFailed {
		t.Errorf("exit = %d, want %d (providers_failed)", got, exitProvidersFailed)
	}

	// The superseded step must not be the reported cause.
	for _, stale := range []string{"getting seasons", "no seasons found"} {
		if strings.Contains(env.Error.Message, stale) {
			t.Errorf("message %q still reports the superseded season step (%q); the resolver is why playback failed",
				env.Error.Message, stale)
		}
	}

	// And the detail the resolver already had must reach the caller without -x,
	// separably per provider.
	if len(env.Error.Providers) != 2 {
		t.Fatalf("error.providers has %d rows, want 2 (one per chain member); envelope: %s",
			len(env.Error.Providers), buf.String())
	}
	seen := map[string]string{}
	for _, p := range env.Error.Providers {
		seen[p.Provider] = p.Error
	}
	for name, want := range map[string]string{
		"deadStreamerAlpha": "status 404 alpha",
		"deadStreamerBeta":  "status 404 beta",
	} {
		got, ok := seen[name]
		if !ok {
			t.Errorf("error.providers names %v, missing %q", seen, name)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("error.providers[%q] = %q, want it to carry %q", name, got, want)
		}
	}
}

// The same defect, one branch over: a primary that enumerates seasons but not
// this season's episodes. resolveAndPlay logs "primary provider episodes
// failed ... trying fallbacks" (cmd/search.go:492) and hands the request to
// tryFallbackStream at cmd/search.go:527 — then, on failure, only debugf'd
// fbErr and fell through to the gate that reports the primary's listing error.
//
// Fed the input that violates the guarantee: the primary's episode error and
// the resolver's failure are different sentences, so a body that reported the
// wrong one is visible.
func TestPlayReportsResolverCauseWhenEpisodeListWasTheSupersededStep(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	buf := captureAgentOut(t)
	withPlayerAvailable(t)

	a, b := newDeadChain("Some Show")
	withFallbackChain(t, a, b)
	withStubProvider(t, &stubProvider{
		seasons:     []media.Season{{ID: "s1", Number: 1}},
		episodesErr: errProviderCannotList,
	})

	withEpisodesFlags(t, tvRef(t, ""), 1)
	prevEpisode := flagEpisode
	flagEpisode = 1
	t.Cleanup(func() { flagEpisode = prevEpisode })

	err := playRun(playCmd, nil)
	if err == nil {
		t.Fatalf("play succeeded with a chain that streams nothing; it emitted %q", buf.String())
	}

	env := decodeErrEnvelope(t, buf.String())
	if got := exitCodeOf(err); got != exitProvidersFailed {
		t.Errorf("exit = %d, want %d (providers_failed)", got, exitProvidersFailed)
	}
	if strings.Contains(env.Error.Message, errProviderCannotList.Error()) {
		t.Errorf("message %q reports the primary's episode-listing error, which resolveAndPlay had already continued past; the resolver is why playback failed",
			env.Error.Message)
	}
	if len(env.Error.Providers) != 2 {
		t.Fatalf("error.providers has %d rows, want 2 (one per chain member); envelope: %s",
			len(env.Error.Providers), buf.String())
	}
}

// episodes' season-gate message blamed the primary's enumeration alone —
// "getting seasons: no seasons found" — while seasonSource had already scanned
// the whole fallback chain under episodesFallbackTimeout and found nothing. A
// reader is told one provider could not answer when eleven were asked, which
// is the same dishonesty play had, milder: it sends them to look at the base
// instead of at the chain.
func TestEpisodesSeasonFailureNamesTheChainScan(t *testing.T) {
	hostileEnv(t)
	buf := captureAgentOut(t)

	// A chain that answers nothing, so seasonSource falls through to its
	// primary-error return — the only path that reaches the message.
	withFallbackChain(t, &stubProvider{})
	withStubProvider(t, &stubProvider{seasonsErr: errors.New("no seasons found")})
	withEpisodesFlags(t, tvRef(t, ""), 0)

	err := episodesRun(episodesCmd, nil)
	if err == nil {
		t.Fatalf("episodes succeeded with a primary and chain that enumerate nothing; it emitted %q", buf.String())
	}
	if got := exitCodeOf(err); got != exitProvidersFailed {
		t.Errorf("exit = %d, want %d", got, exitProvidersFailed)
	}

	env := decodeErrEnvelope(t, buf.String())
	// The chain was asked. The message has to say so, or it is attributing a
	// whole-chain failure to one provider.
	for _, want := range []string{"fallback", "Some Show"} {
		if !strings.Contains(env.Error.Message, want) {
			t.Errorf("message %q does not mention %q; seasonSource scanned the fallback chain before this point and found nothing",
				env.Error.Message, want)
		}
	}
	// And the primary's own reason must survive — it is still half the answer.
	if !strings.Contains(env.Error.Message, "no seasons found") {
		t.Errorf("message %q dropped the primary's reason", env.Error.Message)
	}
}

// resolveFailure must keep the resolver's full sentence as its Error(), so the
// non-JSON callers of tryFallbackStream (cmd/batch.go, makeStreamResolver) read
// exactly as they did before the structured field was added.
func TestResolveFailurePreservesTheResolverSentence(t *testing.T) {
	inner := fmt.Errorf("all providers failed: X: resolve boom")
	rf := &resolveFailure{err: inner}
	if rf.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", rf.Error(), inner.Error())
	}
	if !errors.Is(rf, inner) {
		t.Errorf("errors.Is(resolveFailure, inner) = false; the wrapped cause must stay reachable")
	}
}

// The "%d providers tried" count must not include the resolver's own
// overall-timeout row.
//
// When Resolve's deadline expires it appends an attempt named
// resolver.SyntheticProvider (internal/resolver/resolver.go:84) so the report
// says why it stopped. providersTried counted distinct Attempt.Provider values
// and so counted that note as a provider: a run where two providers were asked
// reported three. These two commits exist to stop this message lying about
// what happened, and an inflated provider count is the same defect — it sends
// the reader looking for a provider that was never probed.
//
// Fed the input that violates the guarantee: a report with real attempts AND
// the synthetic row, not one with only real attempts, which cannot tell a
// correct count from an inflated one.
func TestProvidersTriedExcludesTheResolversOwnTimeoutRow(t *testing.T) {
	rf := &resolveFailure{
		err: errors.New("all providers failed"),
		report: &resolver.Report{Attempts: []resolver.Attempt{
			{Provider: "Alpha", Stage: "resolve", Err: errors.New("status 404")},
			{Provider: "Beta", Stage: "batch-timeout", Err: errors.New("no result within 30s")},
			{Provider: resolver.SyntheticProvider, Stage: resolver.StageOverallTimeout, Err: context.DeadlineExceeded},
		}},
	}
	if got := rf.providersTried(); got != 2 {
		t.Errorf("providersTried() = %d, want 2; the %q row is the resolver reporting its own deadline, not a provider that was asked",
			got, resolver.SyntheticProvider)
	}

	// The row itself stays in the rendered list: "the resolver ran out of
	// time" is the most actionable line a reader can get, and dropping it
	// would leave the envelope silent about why the chain stopped early.
	rows := rf.providerRows()
	if len(rows) != 3 {
		t.Fatalf("providerRows() returned %d rows, want 3 (one per recorded attempt, timeout row included): %+v", len(rows), rows)
	}
	if rows[2]["provider"] != resolver.SyntheticProvider || rows[2]["stage"] != resolver.StageOverallTimeout {
		t.Errorf("providerRows()[2] = %+v, want the %q / %q row preserved", rows[2], resolver.SyntheticProvider, resolver.StageOverallTimeout)
	}
}
