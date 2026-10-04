package cmd

import (
	"context"
	"testing"
	"time"

	"lobster/internal/provider"
)

// assertBudgetCtx asserts ctx is the command's own deadline context, not a
// fresh background one: discovering the sources can itself reach the network
// (liveTVSourcesContext -> tbcplCatalogContext -> Client.LoadMerged), so it
// has to be spent inside provider.LiveLoadBudget rather than ahead of it.
//
// The Deadline() check is the assertion that separates the two orderings. A
// context was always passed once agentLiveSources took one; what the ordering
// decides is whether that context carries the command's deadline.
func assertBudgetCtx(t *testing.T, ctx context.Context) {
	t.Helper()
	if ctx == nil {
		t.Fatal("source discovery was called with a nil context")
	}
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("source discovery got a context with no deadline: the catalog fetch it may perform is then unbounded, and the command can overrun the deadline LiveLoadBudget advertises")
	}
	if remaining := time.Until(dl); remaining <= 0 || remaining > provider.LiveLoadBudget {
		t.Fatalf("source discovery deadline is %v away, want (0, %v]", remaining, provider.LiveLoadBudget)
	}
}

// recordSourcesCtx wraps whatever agentLiveSources currently is so the context
// it is called with can be inspected. The caller must already have installed a
// fixture that registers its own t.Cleanup for agentLiveSources
// (withLiveFixture or usePlaylist) — that cleanup restores this wrapper too.
func recordSourcesCtx(got *context.Context) {
	inner := agentLiveSources
	agentLiveSources = func(ctx context.Context) []string {
		*got = ctx
		return inner(ctx)
	}
}

// channels creates its deadline before discovering sources. If the clock
// started afterwards instead, a cold TBCPL catalog fetch would run outside
// the budget and the command could take the catalog client's own timeout on
// top of the full LiveLoadBudget.
func TestChannelsDiscoversSourcesInsideTheLoadBudget(t *testing.T) {
	withLiveFixture(t, liveFixtureBody)
	var got context.Context
	recordSourcesCtx(&got)

	runAgentCmd(t, channelsCmd)
	assertBudgetCtx(t, got)
}

// The same ordering on the play path, which also discovers sources before the
// --detach fork so that "no sources configured" stays exit 1.
func TestPlayLiveDiscoversSourcesInsideTheLoadBudget(t *testing.T) {
	const body = "#EXTM3U\n#EXTINF:-1 tvg-id=\"a.tv\",Alpha\nhttp://example.invalid/a.m3u8\n"
	ref := refFromPlaylist(t, body, "Alpha")
	usePlaylist(t, body)
	var played string
	stubLivePlayer(t, &played)

	var got context.Context
	recordSourcesCtx(&got)

	runAgentCmd(t, playCmd, "--ref", ref)
	assertBudgetCtx(t, got)
}
