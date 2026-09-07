package cmd

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"lobster/internal/media"
	"lobster/internal/player"
	"lobster/internal/provider"
)

// agentPlayLive hands a resolved live stream to the player. A package var so
// tests can observe what would be played without launching mpv.
var agentPlayLive = func(stream *media.Stream, title string) error {
	pl := player.New(cfg.Player, cfg.AudioLanguage)
	_, err := pl.Play(stream, title, 0, nil)
	return err
}

// playLiveRef plays a live channel ref.
//
// It never enters resolveAndPlay. That path re-searches by title on every
// fallback provider, so a live ref would be handed to FlixHQ as a title query
// and could play a documentary named after the channel.
//
// No history or resume entry is written, and none needs to be suppressed:
// history is written only in playStream (cmd/search.go:598) and saveHistory
// (cmd/session.go:351-366), and this path enters neither. --continue is inert
// for the same reason — its resume lookup keys on e.ID == selected.ID
// (cmd/search.go:565-573), which is never reached. If a later change routes
// live through playStream, both of those become live problems: live IDs are
// the unstable positional ones, so a resume would seek to a stranger's
// position.
func playLiveRef(cmd *cobra.Command, r playRef) error {
	// cmd.Flags().Changed, not the flag's value: passing --season or
	// --episode at all is the usage error for a live ref, regardless of what
	// value was given. A value check let "--season 0" through silently,
	// discarding a flag the caller explicitly supplied instead of rejecting
	// it — the same class of bug as a "--category ''" value check standing
	// in for "was --category passed at all" (cmd/channels.go).
	if cmd.Flags().Changed("season") || cmd.Flags().Changed("episode") {
		return emitErr("usage", exitUsage,
			"%q is a live channel: --season and --episode do not apply", r.Title)
	}
	// cmd.Flags().Changed, not the value: "--download ''" is indistinguishable
	// from unset under a value check, so it would slip through and let live
	// playback proceed uncaught. Mirrors the season/episode guard above.
	if cmd.Flags().Changed("download") {
		return emitErr("unsupported", exitUsage,
			"--download is not supported for live channels")
	}
	if available, name := agentPlayerCheck(); !available {
		return emitErr("player_unavailable", exitPlayerUnavailable,
			"%s is not installed or not on PATH", name)
	}

	// The sources are discovered before the --detach fork. Discovering them
	// is cheap beside loading them (config, plus a TBCPL catalog fetch that
	// is usually a warm cache read), and doing it late is wrong: with no live
	// sources configured, forking first would have the child exit 1 with
	// not_configured, the parent's liveness wait see it die immediately, and
	// playDetached report exit 3 (providers_failed) naming a log file —
	// contradicting the documented contract (README.md,
	// skills/lobster-play/SKILL.md) that not_configured at exit 1 means "tell
	// the user to configure a source", not "retry later". Mirrors the same
	// ordering in cmd/play.go for the non-live path (the base/season-episode
	// checks before its own --detach dispatch).
	//
	// The context is created first so that catalog fetch is bounded by the
	// same budget as the playlist load below, rather than running unmeasured
	// ahead of it. cmd/channels.go makes the same ordering choice, for the
	// same reason.
	ctx, cancel := context.WithTimeout(context.Background(), provider.LiveLoadBudget)
	defer cancel()

	sources := agentLiveSources(ctx)
	if len(sources) == 0 {
		return emitErr("not_configured", exitUsage,
			"no live TV sources configured; add one under [live_tv] in the config, or enable the TBCPL feed")
	}

	// Fork before loading any playlist. supervisorArgs forwards only the ref
	// (cmd/detach.go:86-96), so the child reloads regardless; resolving here
	// would do the work twice and block the parent, breaking the "returns
	// immediately" contract --detach promises. The !flagSupervised guard
	// mirrors cmd/play.go:218 — without it the child forks a child of its own.
	if flagDetach && !flagSupervised {
		return playDetached(cmd, r)
	}

	p := agentLiveTV(sources)
	if err := p.LoadContext(ctx); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}

	ch, err := resolveLiveRef(p, r)
	if err != nil {
		return err
	}

	stream, err := p.Watch(ch.ID, "", "LiveTV", cfgQuality())
	if err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}
	if err := agentPlayLive(stream, ch.Name); err != nil {
		return emitErr("providers_failed", exitProvidersFailed, "%v", err)
	}
	return emitJSON(map[string]any{"status": "finished", "title": ch.Name})
}

// resolveLiveRef re-matches a live ref against the freshly loaded playlists.
//
// The matching rule: tvg-id when present; if that yields more than one
// channel, narrow by exact folded Title; otherwise match by exact folded
// Title directly. Source narrows throughout — but not via ChannelKey.Source:
// a ref does not carry the raw source string at all (it would carry the
// subscriber's Xtream credentials on stdout), so there is nothing to pass
// into Lookup's ChannelKey that could match provider.Channel.Source. Instead
// Lookup is asked by TVGID/Name alone and the result is narrowed here by
// sourceKey, the digest both sides can be reduced to (see liveChannelRef,
// cmd/channels.go). Filtering can only shrink the match set, so a source
// mismatch still yields either the right channel or
// ambiguity/absence, never a wrong pick. It fails closed if the result is
// zero or still more than one — an ID is never authoritative on its own:
// uniqueID (internal/provider/livetv.go:221-232) disambiguates by playlist
// order, so an ID means a position, not a channel, and picking the first
// remaining match on ambiguity would reintroduce exactly the ordering
// dependence this design exists to escape.
//
// The title-narrowing step exists because Lookup itself does not perform it:
// once TVGID is non-empty, Lookup matches on TVGID alone and does not also
// consult Name (ChannelKey doc comment, internal/provider/livetv.go). Real
// playlists are not always disciplined about tvg-id uniqueness — the same
// logical channel at several qualities, or plain provider sloppiness, can
// share one tvg-id across genuinely different channels. Narrowing the
// resulting set by the ref's own recorded Title still resolves the common
// case (a stable tvg-id) correctly after a reorder, using more of the ref's
// own identity rather than picking by position. Two channels sharing BOTH
// tvg-id and title remain genuinely ambiguous and still refuse below.
func resolveLiveRef(p *provider.LiveTV, r playRef) (provider.Channel, error) {
	matches := p.Lookup(provider.ChannelKey{TVGID: r.TVGID, Name: r.Title})
	matches = filterBySource(matches, r)

	if r.TVGID != "" && len(matches) > 1 {
		matches = filterByExactTitle(matches, r.Title)
	}

	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		// The stream URL is not printed here: an Xtream-codes URL embeds the
		// subscriber's username and password in its path (unlike the
		// playlist source string, whose credentials live in query
		// parameters that displaySource can strip), so there is no safe way
		// to redact it. The count is enough to tell the caller this needs a
		// playlist fix (distinct tvg-id values), not a retry.
		return provider.Channel{}, emitErr("ambiguous_channel", exitNoResults,
			"%q matches %d channels and cannot be identified unambiguously; "+
				"the playlist needs distinct tvg-id values for them", r.Title, len(matches))
	}

	// No match. Distinguish "the channel is gone" from "its playlist is down":
	// they call for completely different advice.
	//
	// FailedSources() returns raw source strings while the ref carries only
	// their digest, so each failed source is reduced through sourceKey before
	// the comparison rather than compared as a string. Comparing f == r.Source
	// directly is only wrong for http(s) sources — displaySource is the
	// identity for a local file path, so that case is indistinguishable from
	// the fix under test fixtures that only use temp-file playlists. For a real
	// credentialed Xtream source (an http(s) URL), skipping this would regress
	// every down-playlist ref to "no_results" (exit 2, "the channel does not
	// exist") when "providers_failed" (exit 3, "retry") is correct.
	if refSourceInFailedSources(p.FailedSources(), r) {
		return provider.Channel{}, emitErr("providers_failed", exitProvidersFailed,
			"%q could not be matched: its playlist (%s) failed to load; the channel may still exist",
			r.Title, r.Source)
	}
	return provider.Channel{}, emitErr("no_results", exitNoResults,
		"%q is no longer in your playlists; re-run 'lobster channels' to get a current ref", r.Title)
}

// refSourceInFailedSources reports whether r's playlist is one of the raw
// source strings FailedSources() returns. Pulled out of resolveLiveRef so the
// digest-vs-raw comparison can be unit-tested directly with an http(s)
// source, without needing a real failing provider to reach it — every live-TV
// test fixture otherwise uses temp-file paths, for which the raw string and
// its display form coincide, and so cannot distinguish this comparison from a
// raw-to-raw one.
//
// A ref minted before src_key existed carries only the sanitized Source, so
// that older form is still honoured: it cannot tell two subscriptions on one
// server apart, but the alternative is telling the user their channel no
// longer exists when their playlist is merely down. Same-server collisions
// were the pre-src_key behaviour throughout; refusing to read old refs at all
// would be a larger regression than the one this narrows.
func refSourceInFailedSources(failed []string, r playRef) bool {
	for _, f := range failed {
		if r.SrcKey != "" {
			if sourceKey(f) == r.SrcKey {
				return true
			}
			continue
		}
		if r.Source != "" && displaySource(f) == r.Source {
			return true
		}
	}
	return false
}

// filterBySource narrows chs to those from the playlist r was minted from.
//
// The comparison is on sourceKey, not on the ref's display Source: two Xtream
// subscriptions to one server share a display Source (displaySource drops the
// query string carrying their credentials), so narrowing by it would let a
// ref from one subscription match a channel from the other — see sourceKey
// (cmd/channels.go) for why that is the bug this narrowing exists to prevent.
// The channel's raw Source is reduced through the same function, so both
// sides are digests.
//
// A ref carrying neither key nor source is not narrowed at all, rather than
// narrowed to channels with no source — consistent with ChannelKey.Source's
// own empty-means-unset convention. A ref minted before src_key existed still
// narrows by display Source: weaker (it cannot separate two subscriptions on
// one server) but strictly better than not narrowing, and it is the exact
// behaviour those refs were minted under.
func filterBySource(chs []provider.Channel, r playRef) []provider.Channel {
	if r.SrcKey == "" && r.Source == "" {
		return chs
	}
	out := make([]provider.Channel, 0, len(chs))
	for _, ch := range chs {
		if r.SrcKey != "" {
			if sourceKey(ch.Source) == r.SrcKey {
				out = append(out, ch)
			}
			continue
		}
		if displaySource(ch.Source) == r.Source {
			out = append(out, ch)
		}
	}
	return out
}

// filterByExactTitle narrows chs to those whose Name case/whitespace-folds to
// title. Used only to break a tie among channels that share a tvg-id.
func filterByExactTitle(chs []provider.Channel, title string) []provider.Channel {
	want := strings.ToLower(strings.TrimSpace(title))
	out := make([]provider.Channel, 0, len(chs))
	for _, ch := range chs {
		if strings.ToLower(strings.TrimSpace(ch.Name)) == want {
			out = append(out, ch)
		}
	}
	return out
}
