package cmd

import (
	"testing"

	"lobster/internal/history"
	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

// checkpointPlayProvider extends stubStreamProvider so resolveAndPlay's
// StreamProvider branch finds a server and resolves the canned stream, keeping
// the whole play path off the network and away from the fallback chain.
type checkpointPlayProvider struct{ stubStreamProvider }

func (p *checkpointPlayProvider) GetServers(string, string) ([]media.Server, error) {
	return []media.Server{{Name: "Stub", ID: "srv1"}}, nil
}

// The agent surface (`lobster play --ref`, the owner's primary path and the
// one the motivating hard-shutdown incident happened on) must checkpoint
// mid-playback like the interactive paths. It funnels through
// agentResolveAndPlay -> resolveAndPlay -> playStream, so this drives the real
// playRun end to end (only the provider, player and availability probe are
// stubbed) and asserts the position was on disk while the player was still
// running.
func TestAgentPlayPathCheckpointsPositionMidPlayback(t *testing.T) {
	stub := &stubCheckpointPlayer{
		stubPlayerImpl: stubPlayerImpl{result: player.PlayResult{Position: 1234, Duration: 5400}},
		fire:           true,
		mid:            [2]float64{600, 5400},
	}
	playStreamHarness(t, stub)
	hostileEnv(t)
	captureAgentOut(t)

	prevProv := agentProvider
	agentProvider = func() provider.Provider {
		return &checkpointPlayProvider{stubStreamProvider{
			stream: &media.Stream{URL: "http://127.0.0.1:1/never-dialed.m3u8"},
		}}
	}
	t.Cleanup(func() { agentProvider = prevProv })

	// agentPlayerCheck probes the real configured binary; the stub player is
	// what actually plays, so force the probe to pass regardless of what is
	// installed on the machine running the test.
	prevCheck := agentPlayerCheck
	agentPlayerCheck = func() (bool, string) { return true, "stub" }
	t.Cleanup(func() { agentPlayerCheck = prevCheck })

	ref, err := encodeRef(playRef{ID: "movie/agent", Title: "Agent", Type: "movie"})
	if err != nil {
		t.Fatalf("encodeRef: %v", err)
	}
	prevRef, prevDetach := flagRef, flagDetach
	flagRef, flagDetach = ref, false
	t.Cleanup(func() { flagRef, flagDetach = prevRef, prevDetach })

	if err := playRun(playCmd, nil); err != nil {
		t.Fatalf("playRun: %v", err)
	}

	// The state a hard shutdown mid-watch would have left behind.
	var found bool
	for _, e := range stub.midEntries {
		if e.ID == "movie/agent" {
			found = true
			if e.Position != 600 || e.Duration != 5400 {
				t.Fatalf("mid-playback history = pos %g dur %g, want 600/5400 from the checkpoint", e.Position, e.Duration)
			}
		}
	}
	if !found {
		t.Fatalf("no history entry existed during agent-path playback; a hard shutdown would have lost the position (mid entries: %+v)", stub.midEntries)
	}

	// The exit-time save still wins with the final, more precise position.
	entries, loadErr := history.Load()
	if loadErr != nil {
		t.Fatalf("history.Load: %v", loadErr)
	}
	for _, e := range entries {
		if e.ID == "movie/agent" {
			if e.Position != 1234 {
				t.Fatalf("final history position = %g, want 1234 (exit save must win over the checkpoint)", e.Position)
			}
			return
		}
	}
	t.Fatalf("no final history entry for movie/agent; entries: %+v", entries)
}
