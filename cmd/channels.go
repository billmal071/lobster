package cmd

import (
	"context"
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

	listing := flagChannelsCategory != "" || cmd.Flags().Changed("search")
	if !listing {
		return emitChannelCategories(p)
	}
	return emitChannelRows(p)
}

func emitChannelCategories(p *provider.LiveTV) error {
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

	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "channels": counts[n]})
	}
	return emitJSON(map[string]any{"categories": out})
}

func emitChannelRows(p *provider.LiveTV) error {
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
		return emitErr("no_results", exitNoResults, "no channel matched")
	}
	return emitJSON(map[string]any{"channels": out})
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
