package cmd

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"lobster/internal/config"
	"lobster/internal/media"
	"lobster/internal/provider"
)

const downloadAllEntry = "Download all episodes"

func downloadMode(t *testing.T) {
	t.Helper()
	hostileEnv(t)
	prevCfg := cfg
	cfg = &config.Config{Quality: "1080"}
	t.Cleanup(func() { cfg = prevCfg })
	prevJSON, prevDL := flagJSON, flagDownload
	flagJSON, flagDownload = false, t.TempDir()
	t.Cleanup(func() { flagJSON, flagDownload = prevJSON, prevDL })
}

func throttled() error {
	return fmt.Errorf("%w: probe 429", provider.ErrIncompleteEpisodeList)
}

func firstEpisodes(prefix string, n int) []media.Episode {
	eps := make([]media.Episode, 0, n)
	for i := 1; i <= n; i++ {
		eps = append(eps, media.Episode{ID: fmt.Sprintf("%se%d", prefix, i), Number: i})
	}
	return eps
}

func streamingMember(id string, listed []media.Episode, listErr error) *scanCountingChainProvider {
	p := &scanCountingChainProvider{
		stubProvider: &stubProvider{
			results: []media.SearchResult{{ID: "tv/" + id, Title: "Some Show", Type: media.TV}},
			seasons: []media.Season{{ID: "f1", Number: 1}},
		},
		url: "http://127.0.0.1:1/never-dialed.m3u8",
	}
	if listErr != nil {
		p.stubProvider.episodesWithErr, p.stubProvider.episodesErr = listed, listErr
	} else {
		p.stubProvider.episodes = listed
	}
	return p
}

func oneSeasonPrimary(listed []media.Episode, listErr error) *stubProvider {
	return &stubProvider{seasons: []media.Season{{ID: "s1", Number: 1}}, episodesWithErr: listed, episodesErr: listErr}
}

var errMenuStop = errors.New("stopped at the episode menu")

func episodeMenuOffered(t *testing.T, primary provider.Provider) []string {
	t.Helper()
	var offered []string
	prev := selectItem
	selectItem = func(prompt string, items []string) (int, error) {
		if prompt == "Episode" {
			offered = items
			return 0, errMenuStop
		}
		return 0, nil
	}
	t.Cleanup(func() { selectItem = prev })
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}
	if err := resolveAndPlay(primary, sel, 0, 0); !errors.Is(err, errMenuStop) {
		t.Fatalf("resolveAndPlay = %v, want it to reach the episode menu", err)
	}
	return offered
}

func downloadAll(t *testing.T, primary provider.Provider) {
	t.Helper()
	recordSelections(t, 0)
	sel := media.SearchResult{ID: "tv/1403", Title: "Some Show", Year: "2013", Type: media.TV}
	_ = resolveAndPlay(primary, sel, 0, 0)
}

func TestDownloadAllPassesOverATruncatedFallbackPrefix(t *testing.T) {
	downloadMode(t)
	short, full := streamingMember("short-1", firstEpisodes("a", 1), throttled()), streamingMember("full-1", firstEpisodes("b", 3), nil)
	withFallbackChain(t, short, full)

	downloadAll(t, oneSeasonPrimary(nil, errBoom))

	want := []string{"full-1:1:1", "full-1:1:2", "full-1:1:3"}
	if got := full.episodesAsked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the batch asked for %v, want %v: the later fallback's complete list, not the first fallback's one-episode prefix", got, want)
	}
}

func TestDownloadAllReplacesTheSourcesTruncatedPrefix(t *testing.T) {
	downloadMode(t)
	full := streamingMember("full-1", firstEpisodes("b", 3), nil)
	withFallbackChain(t, full)

	downloadAll(t, oneSeasonPrimary(firstEpisodes("p", 1), throttled()))

	want := []string{"full-1:1:1", "full-1:1:2", "full-1:1:3"}
	if got := full.episodesAsked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the batch asked for %v, want %v: a fallback's complete list in place of the source's one-episode prefix", got, want)
	}
}

func TestDownloadMenuBatchEntries(t *testing.T) {
	cases := []struct {
		name      string
		primary   func() *stubProvider
		chain     func() []provider.Provider
		wantBatch bool
		wantCount int
	}{
		{"the source's prefix that no fallback can confirm", func() *stubProvider { return oneSeasonPrimary(firstEpisodes("p", 2), throttled()) },
			func() []provider.Provider { return nil }, false, 2},
		{"the source's prefix beside a fallback prefix", func() *stubProvider { return oneSeasonPrimary(firstEpisodes("p", 2), throttled()) },
			func() []provider.Provider {
				return []provider.Provider{streamingMember("short-1", firstEpisodes("a", 5), throttled())}
			}, false, 2},
		{"only fallback prefixes", func() *stubProvider { return oneSeasonPrimary(nil, errBoom) },
			func() []provider.Provider {
				return []provider.Provider{streamingMember("short-1", firstEpisodes("a", 2), throttled())}
			}, false, 2},
		{"a prefix whose end the fallback could not re-confirm", func() *stubProvider { return oneSeasonPrimary(nil, errBoom) },
			func() []provider.Provider {
				unconfirmed := fmt.Errorf("%w: %w", provider.ErrIncompleteEpisodeList, provider.ErrUnconfirmedEpisodeList)
				return []provider.Provider{streamingMember("short-1", firstEpisodes("a", 2), unconfirmed)}
			}, false, 2},
		{"the source's complete list", func() *stubProvider {
			return &stubProvider{seasons: []media.Season{{ID: "s1", Number: 1}}, episodes: firstEpisodes("p", 2)}
		},
			func() []provider.Provider { return nil }, true, 2},
		{"the source's list with catalogue gaps", func() *stubProvider { return oneSeasonPrimary(firstEpisodes("p", 2), gapErr()) },
			func() []provider.Provider { return nil }, true, 2},
		{"a fallback's list with catalogue gaps after a fallback prefix", func() *stubProvider { return oneSeasonPrimary(nil, errBoom) },
			func() []provider.Provider {
				return []provider.Provider{streamingMember("short-1", firstEpisodes("a", 1), throttled()), streamingMember("gap-1", firstEpisodes("g", 3), gapErr())}
			}, true, 3},
		{"the source's prefix replaced by a fallback's complete list", func() *stubProvider { return oneSeasonPrimary(firstEpisodes("p", 2), throttled()) },
			func() []provider.Provider {
				return []provider.Provider{streamingMember("full-1", firstEpisodes("b", 4), nil)}
			}, true, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			downloadMode(t)
			captureWarnings(t)
			withFallbackChain(t, c.chain()...)
			offered := episodeMenuOffered(t, c.primary())
			if got := slices.Contains(offered, downloadAllEntry); got != c.wantBatch {
				t.Fatalf("menu = %v; batch entries offered = %v, want %v", offered, got, c.wantBatch)
			}
			episodes := len(offered)
			if c.wantBatch {
				episodes -= 2
			}
			if episodes != c.wantCount {
				t.Fatalf("menu = %v; %d episodes offered, want %d", offered, episodes, c.wantCount)
			}
		})
	}
}

func TestPlayMenuKeepsTheFirstFallbackPrefix(t *testing.T) {
	hostileEnv(t)
	captureWarnings(t)
	prevDL := flagDownload
	flagDownload = ""
	t.Cleanup(func() { flagDownload = prevDL })
	withFallbackChain(t, streamingMember("short-1", firstEpisodes("a", 2), throttled()), streamingMember("full-1", firstEpisodes("b", 4), nil))

	if offered := episodeMenuOffered(t, oneSeasonPrimary(nil, errBoom)); len(offered) != 2 {
		t.Fatalf("menu = %v, want the first fallback's two-episode prefix: playback keeps provider order", offered)
	}
}
