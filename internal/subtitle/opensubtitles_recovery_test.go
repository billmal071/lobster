package subtitle

import (
	"net/http"
	"strings"
	"sync"
	"testing"
)

// One expiry must cost one login, however many downloads were in flight when
// it happened.
//
// The client is shared by every download in the run, so when the token goes
// stale each download in flight is refused with a 401 and each one asks for a
// replacement. If a refresh replaces the token without checking whether the
// token it was asked to replace is still the current one, that is one /login
// per refused download -- and OpenSubtitles documents a limit of one login per
// second, so a herd turns a recoverable expiry into a refusal.
func TestOpenSubtitlesOneExpiryCostsOneLogin(t *testing.T) {
	srv := &osServer{tokens: []string{"stale-token", "fresh-token"}, acceptBearer: "stale-token"}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")

	// One download first, so the whole herd starts from the same cached
	// token rather than racing the initial login.
	if _, err := c.ResolveDownloadURL(1); err != nil {
		t.Fatalf("priming download: %v", err)
	}
	if srv.loginCount() != 1 {
		t.Fatalf("logins = %d after the priming download, want 1", srv.loginCount())
	}

	// Now the cached token goes stale.
	srv.setAcceptBearer("fresh-token")

	const downloads = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, downloads)
	for i := range downloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = c.ResolveDownloadURL(i + 2)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent download %d: %v", i, err)
		}
	}
	if got := srv.loginCount(); got != 2 {
		t.Fatalf("logins = %d for one expiry across %d concurrent downloads, want 2", got, downloads)
	}
}

// A login that failed because the service was briefly unavailable must not
// keep the whole run on the anonymous quota.
//
// The "this login was refused" flag exists so dead credentials cost one round
// trip per run instead of one per episode. Once the client is shared by the
// whole run, that flag makes a single 503 or timeout at the start of a season
// permanent: every later episode downloads anonymously, on a quota small
// enough to stop the season part way through, and says nothing.
func TestOpenSubtitlesTransientLoginFailureIsRetriedLater(t *testing.T) {
	srv := &osServer{
		tokens:        []string{"token-1"},
		loginStatuses: []int{http.StatusServiceUnavailable, http.StatusOK},
	}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	for i := range 2 {
		if _, err := c.ResolveDownloadURL(i + 1); err != nil {
			t.Fatalf("download %d: %v", i+1, err)
		}
	}

	if got := srv.loginCount(); got != 2 {
		t.Fatalf("logins = %d, want 2: the service recovered and the run never asked again", got)
	}
	want := []string{"", "Bearer token-1"}
	if got := srv.auths(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("download Authorization headers = %v, want %v: the second episode stayed on the anonymous quota", got, want)
	}
}

// The retry has to be bounded, or the cache stops preventing the churn it was
// added for: a service that is properly down would cost a refused round trip
// on every subtitle of every episode.
func TestOpenSubtitlesTransientLoginRetriesAreBounded(t *testing.T) {
	srv := &osServer{loginStatus: http.StatusServiceUnavailable}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	for i := range 10 {
		if _, err := c.ResolveDownloadURL(i + 1); err != nil {
			t.Fatalf("download %d: %v", i+1, err)
		}
	}

	if got := srv.loginCount(); got != maxLoginAttempts {
		t.Fatalf("logins = %d across 10 downloads against a dead /login, want %d", got, maxLoginAttempts)
	}
}

// Credentials the service refuses are a different answer from a service that
// did not answer, and must stay permanent: that is what the flag was for.
func TestOpenSubtitlesRejectedCredentialsAreNotRetried(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := &osServer{loginStatus: code}
			newOSServer(t, srv)

			c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
			for i := range 5 {
				if _, err := c.ResolveDownloadURL(i + 1); err != nil {
					t.Fatalf("download %d: %v", i+1, err)
				}
			}
			if got := srv.loginCount(); got != 1 {
				t.Fatalf("logins = %d across 5 downloads after HTTP %d, want 1", got, code)
			}
		})
	}
}

// The retry budget belongs to the run, not to however many downloads happen to
// be asking at once: a retry waits for a download request to have gone out
// since the last failure. Without that, a burst of concurrent downloads that
// all find no token would spend the whole budget in the same instant, against
// a limit of one login per second, and nothing would be left for the episode
// where the service has actually recovered.
//
// White-box and sequential on purpose. The guarantee is about what happens
// between a failure and the next download, and asserting it through
// concurrency would be asserting on goroutine timing.
func TestOpenSubtitlesTransientRetryWaitsForADownload(t *testing.T) {
	srv := &osServer{loginStatus: http.StatusServiceUnavailable}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
	for range 5 {
		if tok := c.bearerToken(); tok != "" {
			t.Fatalf("bearerToken = %q, want empty while /login is failing", tok)
		}
	}
	if got := srv.loginCount(); got != 1 {
		t.Fatalf("logins = %d for 5 token requests with no download in between, want 1", got)
	}

	c.noteDownloadAttempt()
	if tok := c.bearerToken(); tok != "" {
		t.Fatalf("bearerToken = %q, want empty", tok)
	}
	if got := srv.loginCount(); got != 2 {
		t.Fatalf("logins = %d after a download went out, want 2: the retry never happened", got)
	}
}

// The same budget under real concurrency: 24 downloads racing a dead /login
// must stay within the run's bound rather than producing one login each.
func TestOpenSubtitlesTransientLoginHerdStaysBounded(t *testing.T) {
	srv := &osServer{loginStatus: http.StatusServiceUnavailable}
	newOSServer(t, srv)

	c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")

	const downloads = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, downloads)
	for i := range downloads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = c.ResolveDownloadURL(i + 1)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent download %d: %v", i, err)
		}
	}
	got := srv.loginCount()
	if got < 1 || got > maxLoginAttempts {
		t.Fatalf("logins = %d across %d concurrent downloads, want between 1 and %d", got, downloads, maxLoginAttempts)
	}
}

// Dropping to the anonymous quota silently is what makes the failure bad: the
// run keeps working on a quota small enough to stop a season part way through,
// and the error the user eventually sees is a bare HTTP 403.
func TestOpenSubtitlesSaysWhenItDropsToTheAnonymousQuota(t *testing.T) {
	t.Run("rejected credentials", func(t *testing.T) {
		srv := &osServer{loginStatus: http.StatusUnauthorized}
		newOSServer(t, srv)

		c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
		if _, err := c.ResolveDownloadURL(1); err != nil {
			t.Fatalf("download: %v", err)
		}
		msg := c.TakeLoginWarning()
		for _, want := range []string{"anonymous quota", "401", "opensubtitles_username"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("warning %q does not contain %q", msg, want)
			}
		}
		// Once per client: a season must not repeat it per episode.
		if _, err := c.ResolveDownloadURL(2); err != nil {
			t.Fatalf("second download: %v", err)
		}
		if again := c.TakeLoginWarning(); again != "" {
			t.Fatalf("warning repeated: %q", again)
		}
	})

	t.Run("service unavailable", func(t *testing.T) {
		srv := &osServer{loginStatus: http.StatusServiceUnavailable}
		newOSServer(t, srv)

		c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
		if _, err := c.ResolveDownloadURL(1); err != nil {
			t.Fatalf("download: %v", err)
		}
		msg := c.TakeLoginWarning()
		for _, want := range []string{"could not sign in", "anonymous quota", "tried again"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("warning %q does not contain %q", msg, want)
			}
		}
		// The retry on the next download fails too. Once per client: the
		// season must not say it again for every episode it retries on.
		if _, err := c.ResolveDownloadURL(2); err != nil {
			t.Fatalf("second download: %v", err)
		}
		if srv.loginCount() != 2 {
			t.Fatalf("logins = %d, want 2: the second download never retried", srv.loginCount())
		}
		if again := c.TakeLoginWarning(); again != "" {
			t.Fatalf("warning repeated: %q", again)
		}
	})

	// Nothing went wrong, so there is nothing to say.
	t.Run("a working login says nothing", func(t *testing.T) {
		srv := &osServer{tokens: []string{"token-1"}, acceptBearer: "token-1"}
		newOSServer(t, srv)

		c := newOpenSubtitlesWithLogin("stub-key", "stub-user", "stub-pass")
		if _, err := c.ResolveDownloadURL(1); err != nil {
			t.Fatalf("download: %v", err)
		}
		if msg := c.TakeLoginWarning(); msg != "" {
			t.Fatalf("warning = %q, want none", msg)
		}
	})

	// No credentials is the default and is not a degradation.
	t.Run("no credentials says nothing", func(t *testing.T) {
		srv := &osServer{}
		newOSServer(t, srv)

		c := newOpenSubtitlesWithLogin("stub-key", "", "")
		if _, err := c.ResolveDownloadURL(1); err != nil {
			t.Fatalf("download: %v", err)
		}
		if msg := c.TakeLoginWarning(); msg != "" {
			t.Fatalf("warning = %q, want none: staying anonymous without credentials is the default", msg)
		}
	})
}
