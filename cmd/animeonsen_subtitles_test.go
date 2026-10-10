package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
	"lobster/internal/subtitle"
)

// shortListStreamProvider is AnimeOnsen's shape: it measures its episode list
// by probing, so it answers with the prefix it reached *and* an error wrapping
// provider.ErrIncompleteEpisodeList, and it streams that prefix perfectly well.
type shortListStreamProvider struct {
	*stubProvider

	mu          sync.Mutex
	watchedEp   string
	watchCalled bool
}

func (p *shortListStreamProvider) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	p.mu.Lock()
	p.watchedEp, p.watchCalled = episodeID, true
	p.mu.Unlock()
	return &media.Stream{URL: "https://cdn.invalid/" + mediaID + "/" + episodeID + "/manifest.mpd"}, nil
}

func (p *shortListStreamProvider) episodeWatched() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.watchedEp, p.watchCalled
}

// flaggedEpisodes is a measured prefix of n episodes.
func flaggedEpisodes(n int) []media.Episode {
	eps := make([]media.Episode, 0, n)
	for i := 1; i <= n; i++ {
		eps = append(eps, media.Episode{Number: i, Title: fmt.Sprintf("Episode %d", i), ID: fmt.Sprintf("%d", i)})
	}
	return eps
}

// errFlaggedShortList is what AnimeOnsen's GetEpisodes returns alongside its
// measured prefix: the real sentinel, so shortfallFor sees what it sees live.
var errFlaggedShortList = fmt.Errorf("episodes: %w: animeonsen: the episode probe exceeded 4.5s, so 12 is as far as %q could be confirmed",
	provider.ErrIncompleteEpisodeList, "cvYyOlmbfFWvJYWG")

// countingExternalSubs installs a stub on the externalSubs seam and returns a
// pointer to the call count plus the titles it was asked about. No network.
func countingExternalSubs(t *testing.T) (*int, *[]string) {
	t.Helper()
	return countingExternalSubsWithOutcomes(t, []subSourceOutcome{{Name: "SubDL"}})
}

// countingExternalSubsWithOutcomes is the same seam, with control over what the
// sources are reported to have answered, so a run where a search errored can be
// driven through both playback funnels without touching the network.
func countingExternalSubsWithOutcomes(t *testing.T, asked []subSourceOutcome) (*int, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	var titles []string
	prev := externalSubs
	externalSubs = func(title string, season, episode int) ([]media.Subtitle, []subSourceOutcome) {
		mu.Lock()
		calls++
		titles = append(titles, title)
		mu.Unlock()
		return nil, asked
	}
	t.Cleanup(func() { externalSubs = prev })
	return &calls, &titles
}

// A list the provider measured and flagged is an answer, and playback must be
// served from the provider that measured it.
//
// AnimeOnsen returns its twelve episodes together with
// provider.ErrIncompleteEpisodeList, whose documented contract is "here is what
// was measured, do not treat it as the end". resolveAndPlay read any non-nil
// error as "no list", threw the twelve away, handed the show to a fallback
// chain that cannot stream it, and died reporting that the season could not be
// listed. Nothing played — which is also why no external subtitle search ever
// ran against this source, and why its missing subtitles looked like a subtitle
// bug.
func TestResolveAndPlayKeepsAnEpisodeListTheProviderFlaggedAsShort(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.SubsLanguage = "english"
	cfg.SubDLAPIKey = "test-key"

	flagNoSubs = false // the harness pins it on; this test is about the search running

	// Nothing in the chain has this show, so the recovery path has nothing to
	// offer: the only way to a playable stream is the primary's own list.
	withFallbackChain(t)

	warnings := captureWarnings(t)
	subCalls, subTitles := countingExternalSubs(t)

	primary := &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:         []media.Season{{ID: "cvYyOlmbfFWvJYWG", Number: 1}},
		episodesWithErr: flaggedEpisodes(12),
		episodesErr:     errFlaggedShortList,
	}}

	sel := media.SearchResult{ID: "cvYyOlmbfFWvJYWG", Title: "KAMUI: He's Behind You", Type: media.TV}

	// Episode 12 is the last the probe confirmed, so the session ends there and
	// no post-play menu is offered.
	if err := resolveAndPlay(primary, sel, 1, 12); err != nil {
		t.Fatalf("resolveAndPlay = %v; a list the provider measured and flagged must still be played from that provider", err)
	}
	ep, called := primary.episodeWatched()
	if !called {
		t.Fatal("the primary's Watch was never called; playback was handed away from the provider that had the list")
	}
	if ep != "12" {
		t.Fatalf("primary Watch asked for episode %q, want %q", ep, "12")
	}
	if *subCalls != 1 {
		t.Fatalf("external subtitle search ran %d times, want exactly 1", *subCalls)
	}
	if len(*subTitles) != 1 || (*subTitles)[0] != "KAMUI: He's Behind You" {
		t.Fatalf("external subtitle search asked about %v, want [%q]", *subTitles, "KAMUI: He's Behind You")
	}
	// The list may genuinely be short, so keeping it is only honest if the run
	// says so: a silently kept prefix is the fabricated-list bug again.
	if !containsSubstring(*warnings, "may be short") {
		t.Fatalf("warnings = %v; keeping a flagged list must say the list may be short", *warnings)
	}
	if !containsSubstring(*warnings, "as far as") {
		t.Fatalf("warnings = %v; the warning must carry the provider's own reason", *warnings)
	}
}

// The other half of the same rule: a flagged list is not evidence about what
// lies above it, so an episode past the measured prefix must still reach the
// recovery path rather than being refused. Without this, honouring the flag
// would turn "the list may be short" into a refusal of episodes that exist.
func TestResolveAndPlayStillRecoversAnEpisodeAboveAFlaggedList(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})

	sel := media.SearchResult{ID: "1403", Title: "Some Show", Type: media.TV}
	fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
	withFallbackChain(t, fb)

	primary := &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:         []media.Season{{ID: "1403", Number: 1}},
		episodesWithErr: flaggedEpisodes(12),
		episodesErr:     errFlaggedShortList,
	}}

	if err := resolveAndPlay(primary, sel, 1, 13); err != nil {
		t.Fatalf("resolveAndPlay = %v; an episode above a flagged prefix must still be resolved through the chain", err)
	}
	if got := fb.episodeAsked(); got != "1403:1:13" {
		t.Fatalf("chain Watch asked for episode %q, want %q", got, "1403:1:13")
	}
	if _, called := primary.episodeWatched(); called {
		t.Fatal("the primary served episode 13, which its own list does not contain")
	}
}

// The flag travels with a *list*. A provider that returns the flag and nothing
// measured behind it has nothing to keep: warning that a list may be short when
// there is no list, and reporting "no episodes returned" in place of "cannot
// list this season", are both the run describing a state it is not in.
func TestResolveAndPlayDoesNotKeepAFlaggedEmptyEpisodeList(t *testing.T) {
	t.Run("an episode was requested, so the chain gets its turn", func(t *testing.T) {
		hostileEnv(t)
		playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})

		sel := media.SearchResult{ID: "1403", Title: "Some Show", Type: media.TV}
		fb := &recordingStreamProvider{result: sel, url: stubStreamServer(t)}
		withFallbackChain(t, fb)
		warnings := captureWarnings(t)

		if err := resolveAndPlay(flaggedEmptyPrimary(), sel, 1, 2); err != nil {
			t.Fatalf("resolveAndPlay = %v; a flag with no list must still reach the recovery path", err)
		}
		if got := fb.episodeAsked(); got != "1403:1:2" {
			t.Fatalf("chain Watch asked for episode %q, want %q", got, "1403:1:2")
		}
		if containsSubstring(*warnings, "may be short") {
			t.Fatalf("warnings = %v; there was no list to call short", *warnings)
		}
	})

	// No episode requested is the case the non-empty guard is the only thing
	// standing in: the requested-episode check above cannot fire, so dropping
	// the guard clears the error and the refusal changes shape.
	t.Run("no episode requested, so the refusal names the listing failure", func(t *testing.T) {
		hostileEnv(t)
		playStreamHarness(t, &stubPlayerImpl{})
		withFallbackChain(t)
		warnings := captureWarnings(t)

		sel := media.SearchResult{ID: "1403", Title: "Some Show", Type: media.TV}
		err := resolveAndPlay(flaggedEmptyPrimary(), sel, 1, 0)
		if err == nil {
			t.Fatal("resolveAndPlay = nil; there is no list and no episode to resolve, so there is nothing to play")
		}
		if !strings.Contains(err.Error(), "cannot list season 1") {
			t.Fatalf("error = %v; a provider that returned no list must be reported as unable to list the season, not as having returned an empty one", err)
		}
		if containsSubstring(*warnings, "may be short") {
			t.Fatalf("warnings = %v; there was no list to call short", *warnings)
		}
	})
}

// flaggedEmptyPrimary measured nothing and says so: the incomplete-list flag
// with an empty list behind it.
func flaggedEmptyPrimary() *shortListStreamProvider {
	return &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:         []media.Season{{ID: "1403", Number: 1}},
		episodesWithErr: nil,
		episodesErr:     errFlaggedShortList,
	}}
}

// An ordinary, unflagged list must not pay for any of this: one external
// subtitle search, not two.
func TestResolveAndPlayAsksForExternalSubtitlesExactlyOnce(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.SubsLanguage = "english"
	cfg.SubDLAPIKey = "test-key"
	flagNoSubs = false

	withFallbackChain(t)
	subCalls, _ := countingExternalSubs(t)

	primary := &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:  []media.Season{{ID: "s1", Number: 1}},
		episodes: flaggedEpisodes(3),
	}}
	sel := media.SearchResult{ID: "s1", Title: "Some Show", Type: media.TV}

	if err := resolveAndPlay(primary, sel, 1, 3); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if *subCalls != 1 {
		t.Fatalf("external subtitle search ran %d times, want exactly 1", *subCalls)
	}
}

// "We looked and found none" and "we never looked" are different answers, and
// the user could not tell them apart: every attempt line is debug-only, so a
// source that ships no subtitles produced exactly the same silence as an
// unconfigured one.
func TestReportNoSubtitlesSeparatesLookedFromNeverLooked(t *testing.T) {
	boom := errors.New("subdl: 401 unauthorized")
	for _, tc := range []struct {
		name          string
		asked         []subSourceOutcome
		lang          string // "-" means leave subs_language empty
		wantSubstring string
		notSubstring  string
	}{
		{
			name:          "nothing configured",
			wantSubstring: "no external subtitle source is configured",
			notSubstring:  "SubDL",
		},
		{
			name:          "subdl configured and asked",
			asked:         []subSourceOutcome{{Name: "SubDL"}},
			wantSubstring: "SubDL had none for it",
			notSubstring:  "is configured",
		},
		{
			// subs_language can be empty in a config file, and "no  subtitles"
			// with a hole in it is how a reader learns to distrust the line.
			name:          "no language configured",
			asked:         []subSourceOutcome{{Name: "SubDL"}},
			lang:          "-",
			wantSubstring: "no matching subtitles",
			notSubstring:  "is configured",
		},
		{
			name:          "both configured and asked",
			asked:         []subSourceOutcome{{Name: "SubDL"}, {Name: "OpenSubtitles"}},
			wantSubstring: "SubDL and OpenSubtitles had none for it",
			notSubstring:  "is configured",
		},
		{
			// The whole finding: an errored search established nothing, so it
			// must not be reported as the source having none.
			name:          "the only source failed",
			asked:         []subSourceOutcome{{Name: "SubDL", Err: boom}},
			wantSubstring: "the search of SubDL failed, so whether it has any is unknown",
			notSubstring:  "had none",
		},
		{
			name:          "every source failed",
			asked:         []subSourceOutcome{{Name: "SubDL", Err: boom}, {Name: "OpenSubtitles", Err: boom}},
			wantSubstring: "the search of SubDL and OpenSubtitles failed, so whether they have any is unknown",
			notSubstring:  "had none",
		},
		{
			// The sources are asked independently, so the common real case is
			// one of each, and one line has to carry both answers.
			name:          "one had none and one failed",
			asked:         []subSourceOutcome{{Name: "SubDL"}, {Name: "OpenSubtitles", Err: boom}},
			wantSubstring: "SubDL had none for it, and the search of OpenSubtitles failed, so whether it has any is unknown",
			notSubstring:  "SubDL and OpenSubtitles had none",
		},
		{
			// The field name the message tells the user to set has to be the
			// one config actually reads: `os_api_key` is not a Config field,
			// so following this line left OpenSubtitles silently unconfigured.
			name:          "names the real OpenSubtitles config field",
			wantSubstring: "opensubtitles_api_key",
			notSubstring:  "os_api_key",
		},
		{
			// The reachable case. SubDL ships a bundled default key
			// (config.Default), so "nothing is configured" above almost never
			// fires; what a user actually sees is SubDL answering empty. That
			// line named no remaining source, so OpenSubtitles stayed
			// undiscoverable even once the field name above was right.
			name:          "points at the source that was never asked",
			asked:         []subSourceOutcome{{Name: "SubDL"}},
			wantSubstring: "not configured: OpenSubtitles (opensubtitles_api_key)",
			notSubstring:  "SubDL (subdl_api_key)",
		},
		{
			name:          "a failed search still points at the unasked source",
			asked:         []subSourceOutcome{{Name: "SubDL", Err: boom}},
			wantSubstring: "not configured: OpenSubtitles (opensubtitles_api_key)",
			notSubstring:  "had none",
		},
		{
			// Nothing left to suggest: both were asked.
			name:          "suggests nothing when every source was asked",
			asked:         []subSourceOutcome{{Name: "SubDL"}, {Name: "OpenSubtitles"}},
			wantSubstring: "had none for it",
			notSubstring:  "not configured",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			playStreamHarness(t, &stubPlayerImpl{})
			cfg.SubsLanguage = "english"
			if tc.lang == "-" {
				cfg.SubsLanguage = ""
			}
			warnings := captureWarnings(t)

			reportNoSubtitles("KAMUI: He's Behind You", tc.asked)

			if len(*warnings) != 1 {
				t.Fatalf("warnings = %v, want exactly one line", *warnings)
			}
			got := (*warnings)[0]
			if !strings.Contains(got, tc.wantSubstring) {
				t.Fatalf("warning %q does not contain %q", got, tc.wantSubstring)
			}
			if strings.Contains(got, tc.notSubstring) {
				t.Fatalf("warning %q must not contain %q", got, tc.notSubstring)
			}
			if !strings.Contains(got, cfg.SubsLanguage) || !strings.Contains(got, "KAMUI: He's Behind You") {
				t.Fatalf("warning %q must name the language and the title", got)
			}
		})
	}
}

// A playback that ends up with no subtitle file at all must say so. This is the
// AnimeOnsen case end to end: the source carries no text track, SubDL is
// configured and asked, and it has nothing for this title.
func TestPlaybackWithNoSubtitlesAnywhereSaysSo(t *testing.T) {
	hostileEnv(t)
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.SubsLanguage = "english"
	cfg.SubDLAPIKey = "test-key"
	flagNoSubs = false

	withFallbackChain(t)
	countingExternalSubs(t)
	warnings := captureWarnings(t)

	primary := &shortListStreamProvider{stubProvider: &stubProvider{
		seasons:  []media.Season{{ID: "s1", Number: 1}},
		episodes: flaggedEpisodes(1),
	}}
	sel := media.SearchResult{ID: "s1", Title: "KAMUI: He's Behind You", Type: media.TV}

	if err := resolveAndPlay(primary, sel, 1, 1); err != nil {
		t.Fatalf("resolveAndPlay = %v", err)
	}
	if !containsSubstring(*warnings, "SubDL had none for it") {
		t.Fatalf("warnings = %v; a playback with no subtitles at all must say it looked and found none", *warnings)
	}
}

func containsSubstring(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// playStream is the other funnel — the movie path and every fallback-resolved
// play — and it has its own copy of the merge/limit/download block. A report
// wired into only one of the two leaves half the program silent, and no test of
// resolveSubtitles alone can see that.
func TestPlayStreamWithNoSubtitlesAnywhereSaysSo(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.SubsLanguage = "english"
	cfg.SubDLAPIKey = "test-key"
	flagNoSubs = false

	subCalls, subTitles := countingExternalSubs(t)
	warnings := captureWarnings(t)

	stream := &media.Stream{URL: "https://cdn.invalid/x/1/manifest.mpd"}
	sel := media.SearchResult{ID: "movie/x", Title: "KAMUI: He's Behind You", Type: media.Movie}

	if err := playStream(stream, "KAMUI: He's Behind You", sel, 0, 0); err != nil {
		t.Fatalf("playStream = %v", err)
	}
	if *subCalls != 1 {
		t.Fatalf("external subtitle search ran %d times, want exactly 1", *subCalls)
	}
	if len(*subTitles) != 1 || (*subTitles)[0] != "KAMUI: He's Behind You" {
		t.Fatalf("external subtitle search asked about %v, want [%q]", *subTitles, "KAMUI: He's Behind You")
	}
	if !containsSubstring(*warnings, "SubDL had none for it") {
		t.Fatalf("warnings = %v; playStream must also report that it looked and found none", *warnings)
	}
}

// The complement, and the thing a "say when there are none" change is most
// likely to break: a playback that does have a subtitle must not be told it has
// none. Without this the report could fire unconditionally and every test above
// would still pass.
func TestPlayStreamWithSubtitlesSaysNothing(t *testing.T) {
	playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
	cfg.SubsLanguage = "english"
	cfg.SubDLAPIKey = "test-key"
	flagNoSubs = false

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	countingExternalSubs(t)
	warnings := captureWarnings(t)

	// No network: the stub writes what the real downloader would have fetched.
	prevDownload := subtitleDownload
	subtitleDownload = func(td *subtitle.TempDir, sub media.Subtitle, season, episode int) (string, error) {
		path := filepath.Join(td.Path(), "stub.srt")
		return path, os.WriteFile(path, []byte("1\n"), 0o644)
	}
	t.Cleanup(func() { subtitleDownload = prevDownload })

	stream := &media.Stream{
		URL:       "https://cdn.invalid/x/1/index.m3u8",
		Subtitles: []media.Subtitle{{URL: "https://cdn.invalid/a.srt", Label: "English", Language: "english"}},
	}
	sel := media.SearchResult{ID: "movie/x", Title: "Some Film", Type: media.Movie}

	if err := playStream(stream, "Some Film", sel, 0, 0); err != nil {
		t.Fatalf("playStream = %v", err)
	}
	if containsSubstring(*warnings, "no english subtitles") {
		t.Fatalf("warnings = %v; this playback had a subtitle", *warnings)
	}
}

// Both playback funnels have to carry the per-source outcome all the way to the
// report, and each has its own copy of the merge block. A funnel that drops it
// falls back to the zero value — no error — which is exactly the unearned
// "had none for it" the report exists to retire, so the loss is silent.
func TestPlaybackReportsAFailedSubtitleSearchAsFailed(t *testing.T) {
	failed := []subSourceOutcome{{Name: "SubDL", Err: errors.New("subdl: 401 unauthorized")}}

	t.Run("playStream", func(t *testing.T) {
		playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
		cfg.SubsLanguage = "english"
		cfg.SubDLAPIKey = "test-key"
		flagNoSubs = false

		countingExternalSubsWithOutcomes(t, failed)
		warnings := captureWarnings(t)

		stream := &media.Stream{URL: "https://cdn.invalid/x/1/manifest.mpd"}
		sel := media.SearchResult{ID: "movie/x", Title: "KAMUI: He's Behind You", Type: media.Movie}

		if err := playStream(stream, "KAMUI: He's Behind You", sel, 0, 0); err != nil {
			t.Fatalf("playStream = %v", err)
		}
		assertFailedSearchReported(t, *warnings)
	})

	t.Run("resolveAndPlay", func(t *testing.T) {
		hostileEnv(t)
		playStreamHarness(t, &stubPlayerImpl{result: player.PlayResult{Position: 10, Duration: 100}})
		cfg.SubsLanguage = "english"
		cfg.SubDLAPIKey = "test-key"
		flagNoSubs = false

		withFallbackChain(t)
		countingExternalSubsWithOutcomes(t, failed)
		warnings := captureWarnings(t)

		primary := &shortListStreamProvider{stubProvider: &stubProvider{
			seasons:  []media.Season{{ID: "s1", Number: 1}},
			episodes: flaggedEpisodes(1),
		}}
		sel := media.SearchResult{ID: "s1", Title: "KAMUI: He's Behind You", Type: media.TV}

		if err := resolveAndPlay(primary, sel, 1, 1); err != nil {
			t.Fatalf("resolveAndPlay = %v", err)
		}
		assertFailedSearchReported(t, *warnings)
	})
}

func assertFailedSearchReported(t *testing.T, warnings []string) {
	t.Helper()
	if !containsSubstring(warnings, "the search of SubDL failed") {
		t.Fatalf("warnings = %v; a search that errored must be reported as a failure, not as absence", warnings)
	}
	if containsSubstring(warnings, "had none for it") {
		t.Fatalf("warnings = %v; SubDL never answered, so it did not establish that it has none", warnings)
	}
}

// searchExternalSubs is what decides who was asked and what each one answered,
// and the error status it records is the only input the report has. Driven for
// real through the two search seams, so no network is involved.
func TestSearchExternalSubsRecordsEachSourcesErrorStatus(t *testing.T) {
	boom := errors.New("subdl: 401 unauthorized")
	one := []media.Subtitle{{URL: "https://cdn.invalid/a.srt", Label: "English", Language: "english"}}

	for _, tc := range []struct {
		name         string
		subdl, os    string
		subdlSubs    []media.Subtitle
		subdlErr     error
		osSubs       []media.Subtitle
		osErr        error
		wantSubs     int
		wantOutcomes []string // "name" or "name!" when the search must be recorded as failed
	}{
		{
			name:         "nothing configured asks nobody",
			wantOutcomes: nil,
		},
		{
			name:         "a source that answers empty is recorded as a completed search",
			subdl:        "k",
			wantOutcomes: []string{"SubDL"},
		},
		{
			name:         "a source that errors is recorded as failed",
			subdl:        "k",
			subdlErr:     boom,
			wantOutcomes: []string{"SubDL!"},
		},
		{
			name:         "the sources are independent",
			subdl:        "k",
			os:           "o",
			subdlErr:     boom,
			osSubs:       one,
			wantSubs:     1,
			wantOutcomes: []string{"SubDL!", "OpenSubtitles"},
		},
		{
			// An unconfigured source was never asked, so it must not appear at
			// all — otherwise the report would claim it had none.
			name:         "only configured sources are reported",
			os:           "o",
			osErr:        boom,
			wantOutcomes: []string{"OpenSubtitles!"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			playStreamHarness(t, &stubPlayerImpl{})
			cfg.SubsLanguage = "english"
			cfg.SubDLAPIKey, cfg.OSAPIKey = tc.subdl, tc.os

			prevSubdl, prevOS := subdlSearch, osSearch
			t.Cleanup(func() { subdlSearch, osSearch = prevSubdl, prevOS })
			subdlSearch = func(key, title, lang string, season, episode int) ([]media.Subtitle, error) {
				if key != tc.subdl {
					t.Errorf("SubDL asked with key %q, want %q", key, tc.subdl)
				}
				return tc.subdlSubs, tc.subdlErr
			}
			osSearch = func(key, title, lang string, season, episode int) ([]media.Subtitle, error) {
				if key != tc.os {
					t.Errorf("OpenSubtitles asked with key %q, want %q", key, tc.os)
				}
				return tc.osSubs, tc.osErr
			}

			subs, asked := searchExternalSubs("KAMUI: He's Behind You", 1, 1)
			if len(subs) != tc.wantSubs {
				t.Fatalf("subtitles = %d, want %d", len(subs), tc.wantSubs)
			}
			var got []string
			for _, o := range asked {
				if o.Err != nil {
					got = append(got, o.Name+"!")
					continue
				}
				got = append(got, o.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.wantOutcomes, ",") {
				t.Fatalf("outcomes = %v, want %v", got, tc.wantOutcomes)
			}
		})
	}
}
