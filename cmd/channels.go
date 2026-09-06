package cmd

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/provider"
)

var (
	flagChannelsCategory string
	flagChannelsSearch   string
	flagChannelsLimit    int
)

// agentLiveTV builds the Live TV provider. A package var so tests can supply
// one over a temp-file playlist instead of one that reaches the network.
var agentLiveTV = func(sources []string) *provider.LiveTV { return provider.NewLiveTV(sources) }

// agentLiveSources is the configured source list, seamed for the same reason.
var agentLiveSources = liveTVSources

var channelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "List live TV channels and print JSON (no prompts)",
	Long: `List live TV categories, or the channels in one, as JSON on stdout.

With no flags it prints the categories and how many channels each holds. With
--category or --search it prints matching channels, each carrying an opaque
"ref" which is the handle to pass to "lobster play --ref".

Categories come from the playlists' own group-title text, so --category matches
case-insensitively and the output echoes the playlist's spelling.
"Uncategorized" is synthesised for channels whose playlist gives no group. A
channel listed under several groups is counted in each, so the category counts
can sum to more than the number of distinct channels.`,
	Args: cobra.NoArgs, // markAgentCommand would otherwise install ArbitraryArgs
	RunE: channelsRun,
}

func init() {
	markAgentCommand(channelsCmd)
	channelsCmd.Flags().StringVar(&flagChannelsCategory, "category", "", "Only channels in this category (case-insensitive)")
	channelsCmd.Flags().StringVar(&flagChannelsSearch, "search", "", "Only channels whose name contains this text (case-insensitive)")
	channelsCmd.Flags().IntVar(&flagChannelsLimit, "limit", 0, "Maximum channels to print (0 = no limit)")
}

func channelsRun(cmd *cobra.Command, args []string) error {
	sources := agentLiveSources()
	if len(sources) == 0 {
		return emitErr("not_configured", exitUsage,
			"no live TV sources configured; add one under [live_tv] in the config, or enable the TBCPL feed")
	}

	p := agentLiveTV(sources)
	ctx, cancel := context.WithTimeout(context.Background(), provider.LiveLoadBudget)
	defer cancel()
	if err := p.LoadContext(ctx); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	// cmd.Flags().Changed, not the value: an explicit `--category ""` is a
	// request for the row-listing view (matching everything, exactly like
	// `--search ""` already does below), not "the flag was never given".
	// Comparing to the empty string here previously made --category and
	// --search inconsistent with each other for no reason.
	listing := cmd.Flags().Changed("category") || cmd.Flags().Changed("search")

	// FailedSources must be consulted before treating an empty result as
	// "nothing matched": LoadContext only errors when every source fails, so
	// with several playlists configured and one down, a channel that lives
	// only on the dead one would otherwise vanish silently and read as
	// exit 2 ("give up, it isn't there") when the truth is exit 3 ("a
	// playlist is down, try again"). This is the reason channels exists
	// separately from find rather than as a flag on it.
	failed := p.FailedSources()
	if !listing {
		return emitChannelCategories(p, failed)
	}
	return emitChannelRows(p, failed)
}

func emitChannelCategories(p *provider.LiveTV, failed []string) error {
	failed = sanitizeFailedSources(failed)
	counts := map[string]int{}
	for _, ch := range p.AllChannels() {
		for _, c := range ch.Categories {
			counts[c]++
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)

	if len(names) == 0 && len(failed) > 0 {
		return emitErr("providers_failed", exitProvidersFailed,
			"no channels loaded; failed source(s): %s", strings.Join(failed, ", "))
	}

	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "channels": counts[n]})
	}
	payload := map[string]any{"categories": out}
	if len(failed) > 0 {
		payload["failed_sources"] = failed
	}
	return emitJSON(payload)
}

func emitChannelRows(p *provider.LiveTV, failed []string) error {
	failed = sanitizeFailedSources(failed)
	wantCat := strings.ToLower(strings.TrimSpace(flagChannelsCategory))
	wantName := strings.ToLower(strings.TrimSpace(flagChannelsSearch))

	out := []map[string]any{}
	for _, ch := range p.AllChannels() {
		matchedCat := ""
		if wantCat == "" {
			if len(ch.Categories) > 0 {
				matchedCat = ch.Categories[0]
			}
		} else {
			for _, c := range ch.Categories {
				if strings.ToLower(strings.TrimSpace(c)) == wantCat {
					matchedCat = c
					break
				}
			}
			if matchedCat == "" {
				continue
			}
		}
		if wantName != "" && !strings.Contains(strings.ToLower(ch.Name), wantName) {
			continue
		}
		ref, err := liveChannelRef(ch)
		if err != nil {
			return emitErr("internal", 1, "encoding ref: %v", err)
		}
		out = append(out, map[string]any{
			"name":     ch.Name,
			"category": matchedCat,
			"logo":     ch.Logo,
			"ref":      ref,
		})
		if flagChannelsLimit > 0 && len(out) >= flagChannelsLimit {
			break
		}
	}
	if len(out) == 0 {
		if len(failed) > 0 {
			return emitErr("providers_failed", exitProvidersFailed,
				"no channel matched; failed source(s): %s", strings.Join(failed, ", "))
		}
		return emitErr("no_results", exitNoResults, "no channel matched")
	}
	payload := map[string]any{"channels": out}
	if len(failed) > 0 {
		payload["failed_sources"] = failed
	}
	return emitJSON(payload)
}

// displaySource renders a live TV source for user- or agent-facing output
// (error messages, JSON envelopes, and any future caller — e.g. a later
// play --ref failure naming its playlist should route through this same
// function rather than growing a second, divergent sanitizer).
//
// internal/config/config.go's Xtream URL builder embeds the subscriber's
// username and password as query parameters
// ("<server>/get.php?username=...&password=...&type=m3u_plus&output=m3u8"),
// and FailedSources() returns that string verbatim. So an http(s) source is
// reduced to scheme://host/path: userinfo and the *entire* query string are
// dropped, not just named parameters — masking individual parameters would
// miss a credential a future source scheme adds under a different name.
// Host+path is still enough for a caller to tell "the iptv-org feed" from
// "my Xtream server" apart.
//
// Local file paths are returned unchanged. They carry no query string and
// the path itself is the natural identifier for "which playlist is down" —
// a user's own configured path appearing in their own tool's output is not a
// credential disclosure. This is a deliberate decision, not an oversight:
// do not redact local paths too.
func displaySource(s string) string {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "<url>"
	}
	u.RawQuery = ""
	u.User = nil
	return u.String()
}

// sanitizeFailedSources maps FailedSources() output through displaySource.
// Called once, at the top of each emit function, so every path that reports
// a failed source — the error message and the success envelope alike — goes
// through it; nothing downstream ever sees the raw source strings.
func sanitizeFailedSources(failed []string) []string {
	if len(failed) == 0 {
		return failed
	}
	out := make([]string, len(failed))
	for i, s := range failed {
		out[i] = displaySource(s)
	}
	return out
}

// liveChannelRef mints the ref for one channel. It carries TVGID and Source so
// play can re-match the channel after a reload rather than trusting an ID that
// depends on playlist order.
func liveChannelRef(ch provider.Channel) (string, error) {
	return encodeRef(playRef{
		ID:     ch.ID,
		Title:  ch.Name,
		Type:   liveRefType,
		TVGID:  ch.TVGID,
		Source: ch.Source,
	})
}
