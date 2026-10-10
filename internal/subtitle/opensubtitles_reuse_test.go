package subtitle

import (
	"net/http"
	"sync"
	"testing"
)

// The download path builds its client per download, so "one login per client"
// is not the same guarantee as "one login per season": a 12-episode run that
// constructs a client an episode logs in 12 times, and the loginFailed flag
// that is meant to stop a dead credential pair costing a round trip per
// episode lives on the client it is thrown away with.
//
// These tests therefore count /login across episodes, through the same entry
// point the download path uses, instead of across downloads on one hand-held
// client.

// resetOpenSubtitlesReuse empties the shared client slot and restores it
// afterwards, so one test's cached session cannot answer another's.
func resetOpenSubtitlesReuse(t *testing.T) {
	t.Helper()
	sharedOpenSubtitles.mu.Lock()
	prevCreds, prevClient := sharedOpenSubtitles.creds, sharedOpenSubtitles.client
	sharedOpenSubtitles.creds, sharedOpenSubtitles.client = osCredentials{}, nil
	sharedOpenSubtitles.mu.Unlock()
	t.Cleanup(func() {
		sharedOpenSubtitles.mu.Lock()
		defer sharedOpenSubtitles.mu.Unlock()
		sharedOpenSubtitles.creds, sharedOpenSubtitles.client = prevCreds, prevClient
	})
}

// Three episodes of a season, each resolving its own subtitle, must cost one
// login between them.
func TestOpenSubtitlesSeasonLogsInOnceAcrossEpisodes(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1"}, acceptBearer: "token-1"}
	newOSServer(t, srv)
	resetOpenSubtitlesReuse(t)

	for ep := 1; ep <= 3; ep++ {
		c := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass")
		if _, err := c.ResolveDownloadURL(ep); err != nil {
			t.Fatalf("episode %d: %v", ep, err)
		}
	}
	if got := srv.loginCount(); got != 1 {
		t.Fatalf("logins = %d across 3 episodes, want 1", got)
	}
}

// Dead credentials are the worse half: a login that is refused must be
// refused once for the whole run, not re-attempted for every episode. The
// downloads still have to succeed anonymously.
func TestOpenSubtitlesDeadCredentialsCostOneLoginPerRun(t *testing.T) {
	srv := &osServer{tokens: []string{"unused"}, loginStatus: http.StatusUnauthorized}
	newOSServer(t, srv)
	resetOpenSubtitlesReuse(t)

	for ep := 1; ep <= 4; ep++ {
		c := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass")
		if _, err := c.ResolveDownloadURL(ep); err != nil {
			t.Fatalf("episode %d still has to download anonymously: %v", ep, err)
		}
	}
	if got := srv.loginCount(); got != 1 {
		t.Fatalf("logins = %d across 4 episodes with dead credentials, want 1", got)
	}
}

// Reuse is keyed on the credentials, not on "the first client ever built":
// cfg is a global here and tests mutate it, so a client retained regardless of
// what the credentials now say would hand back a session for an account the
// run is no longer configured for.
//
// The cache holds one entry, so going back to an earlier pair is a fresh
// client and a fresh login. That is the deliberate trade: a process has one
// cfg, so capacity beyond one buys nothing and would keep a second password
// alive for the lifetime of the process.
func TestOpenSubtitlesReuseFollowsChangedCredentials(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1", "token-2", "token-3"}}
	newOSServer(t, srv)
	resetOpenSubtitlesReuse(t)

	first := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass")
	if _, err := first.ResolveDownloadURL(1); err != nil {
		t.Fatalf("first download: %v", err)
	}
	if again := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass"); again != first {
		t.Fatal("same credentials returned a different client, so the season logs in again")
	}
	if got := srv.loginCount(); got != 1 {
		t.Fatalf("logins = %d for two downloads on one credential pair, want 1", got)
	}

	changed := OpenSubtitlesFor("stub-key", "stub-user", "other-pass")
	if changed == first {
		t.Fatal("changed credentials returned the client built for the old ones")
	}
	if _, err := changed.ResolveDownloadURL(2); err != nil {
		t.Fatalf("download after credential change: %v", err)
	}
	if got := srv.loginCount(); got != 2 {
		t.Fatalf("logins = %d after a credential change, want 2", got)
	}

	if back := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass"); back == first {
		t.Fatal("the evicted credential pair handed back its old client")
	}
}

// Up to three subtitles are downloaded per episode and nothing serialises
// them, so the shared slot is read and written concurrently. Must be clean
// under -race, and must still be one login.
func TestOpenSubtitlesReuseIsConcurrencySafe(t *testing.T) {
	srv := &osServer{tokens: []string{"token-1"}, acceptBearer: "token-1"}
	newOSServer(t, srv)
	resetOpenSubtitlesReuse(t)

	const n = 24
	clients := make([]*OpenSubtitlesClient, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c := OpenSubtitlesFor("stub-key", "stub-user", "stub-pass")
			clients[i] = c
			if _, err := c.ResolveDownloadURL(i + 1); err != nil {
				t.Errorf("concurrent download %d: %v", i, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, c := range clients {
		if c != clients[0] {
			t.Fatalf("goroutine %d got a different client; concurrent downloads do not share one", i)
		}
	}
	if got := srv.loginCount(); got != 1 {
		t.Fatalf("logins = %d for %d concurrent downloads, want 1", got, n)
	}
}
