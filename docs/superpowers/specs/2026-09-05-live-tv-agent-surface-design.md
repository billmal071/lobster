# Live TV on the agent surface: `channels` + live refs

Date: 2026-09-05
Status: approved design, not yet implemented

## Problem

The agent surface shipped in #43 covers movies and TV episodes and explicitly
deferred live TV to this spec. The deferral was not about effort. Three things
had to be answered first, and this document answers them.

**1. Channel IDs are not stable across processes.** `doLoad`
(`internal/provider/livetv.go:127-152`) assigns each channel `p.uniqueID(ch.ID)`,
and `uniqueID` (`livetv.go:161-172`) resolves a collision by appending `-2`,
`-3` in playlist iteration order. That order comes from remote playlists
refetched per process, merged across `cfg.LiveTV.Sources()` plus the TBCPL feed
(`cmd/fallback.go:50-68`), where a failed source is silently skipped. So
`bbc-one-2` is not a name for a channel; it is a name for a position. A
`channels` → `play` round trip across two processes can select a different
channel than the one the user was shown.

**2. Channels claim to be movies.** `channelResult` (`livetv.go:188-190`) stamps
every channel `media.Movie`. The type gate added in #43 reads that as "this ref
is a film", and `play`'s normal path re-searches by title across the whole
fallback chain (`resolveAndPlay`, `cmd/search.go`). A ref for "BBC One" would
therefore be handed to FlixHQ as a title query, and FlixHQ has documentaries
with that in the name. The failure is silent and it plays the wrong thing.

**3. `LiveTV.GetSeasons` fabricates a season.** It returns
`[]media.Season{{Number: 1, ID: id}}` (`livetv.go:250`) so the TUI has something
to descend into. `validateSeasonEpisode` (`cmd/play.go:120`) treats a non-empty
season list as authoritative, so that gate would *approve* a live ref rather
than reject it.

There is also no `cmd/`-layer LiveTV wiring at all. `LiveTV` is constructed only
for the TUI (`cmd/search.go:52`) and driven by `surf` (`cmd/surf.go:90`).

## Scope

**In:** browsing channels and playing one, non-interactively, from an agent.

**Out:** EPG / "what's on now". That needs playlist-header capture, an XMLTV
fetch of 10-50 MB, a parse, a cache, a `tvg-id` join and wall-clock now/next
queries — a subsystem, not a feature. `ParseM3U` currently discards the
`#EXTM3U` header line that carries `url-tvg`/`x-tvg-url`
(`internal/provider/m3u.go:42-43`), so even the EPG URL is not retained today.
It gets its own spec.

## Architecture

Two commands, split where the backends differ and shared where they do not.

Discovery is separate. `find` races roughly eleven providers under a 5s context
(`cmd/multisearch.go`); live TV is a single 60s playlist fetch with a TLS 1.2
retry (`livetv.go:47-60`), reading config the user may never have set. They do
not share a ranking, a limit, or a failure vocabulary. Most decisively, live has
a state `find`'s exit codes cannot express: **no sources configured**.
`liveTVSources()` returns an empty slice in that case and `doLoad` treats zero
sources as success-with-nothing, so folding live into `find` would report it as
exit 2 "nothing matched" — sending the agent to rephrase a query when the fix is
a config line.

Playback is shared. `play --ref` already owns the player-availability check, the
detach supervisor, the log file and the JSON envelope. The supervisor is built
on that specific verb: `supervisorArgs` (`cmd/detach.go:86-95`) hardcodes
`[]string{exe, "play", "--ref", ref, "--supervised"}` and `forwardedArgs` walks
flag tables bound to it. A second playback command would mean a second copy of
235 lines of process-spawn, liveness-wait and log plumbing, to express a
distinction the agent cannot act on — the ref is opaque to it by contract.

## The live ref

`playRef` (`cmd/ref.go`) gains two fields, populated only for live refs:

```go
TVGID  string `json:"tvg_id,omitempty"`
Source string `json:"source,omitempty"`
```

`Type` is the string `"live"`, and `decodeRef` accepts it as a third value
alongside `movie` and `tv`.

`media.MediaType` is **not** extended. The live branch never constructs a
`media.SearchResult` for the resolver — it goes straight to `LiveTV.Watch` — so
a `media.Live` constant would buy nothing while rippling through the TUI and the
resolver. `channelResult`'s `media.Movie` stamp stays confined to the live
provider and the TUI, where it already lives and is already load-bearing.

### Re-match on play, fail closed

`resolveLiveRef` reloads the playlists and matches in this order:

1. `TVGID` equal to a channel's `TVGID` — the stable upstream identifier;
2. failing that, `Title` equal to a channel's `Name`, trimmed and case-folded;
3. failing that, **`no_results`, exit 2.**

`ID` is tried first as a fast path but is never authoritative. That is what makes
`uniqueID`'s instability harmless rather than a bug to fix: the ref does not
depend on the ID surviving a reload.

`Source` is advisory. It narrows the candidate set when a channel name appears in
several playlists, and is ignored when that playlist is no longer configured or
failed to load.

There is no fuzzy match, no widening, and no fallback chain. **An unmatched live
ref fails.** This is the design's central safety property: it makes the "BBC One
plays a FlixHQ documentary" failure structurally unreachable rather than
guarded against.

### `Channel.TVGID`

`parseExtinf` (`m3u.go:56-70`) currently collapses the two sources of identity:

```go
if id := attrs["tvg-id"]; id != "" {
    c.ID = id
} else {
    c.ID = slug(c.Name)
}
```

After this, nothing can tell whether `ID` is an upstream identifier or a slug
derived from a display name. `Channel` gains `TVGID string`, set only when the
`tvg-id` attribute is actually present and non-empty. `ID` keeps its current
meaning and its current callers.

## Command surface

```
lobster channels
  → {"schema":1,"categories":[{"name":"Sports","channels":412}, …]}

lobster channels --category Sports
  → {"schema":1,"channels":[{"name":"BBC One","category":"Sports",
                             "logo":"https://…","ref":"…"}, …]}

lobster channels --search bbc
  → same shape; combines with --category (intersection)

lobster play --ref <live-ref> --detach
  → the existing play envelope, unchanged
```

Channel IDs are never printed. The ref is the only handle, exactly as `find`
already does it — a caller that cannot see an ID cannot come to depend on one.

`channels` is registered with `markAgentCommand` (`cmd/agentjson.go:88`) so its
pre-`RunE` failures — unknown flag, bad `--quality` — emit the envelope like the
other three.

## Data flow

```
lobster channels --search bbc
  cfg → liveTVSources() → agentLiveTV(sources) → LiveTV.load()
      → filter by category and/or name
      → encodeRef{Type:"live", ID, Title, TVGID, Source} per row
      → emitJSON

lobster play --ref <live> --detach
  decodeRef → Type == "live"?
      ├─ yes → reject --season/--episode/--download
      │        → agentPlayerCheck
      │        → resolveLiveRef → Channel
      │        → LiveTV.Watch(ch.ID) → media.Stream
      │        → detach supervisor / player.Play    (no history write)
      └─ no  → existing path: applyRefBase → validateSeasonEpisode
                               → agentResolveAndPlay
```

The type branch is the **first** thing `playRun` does after decoding, before
`applyRefBase` and before `validateSeasonEpisode`. Ordering is load-bearing:
`LiveTV.GetSeasons` fabricates a season, so a live ref reaching that gate would
be approved by it.

No history or resume entry is written for a live ref. A live stream has no
position to resume to, and `playerTracksPosition` (`cmd/detach.go:230`) already
gates the detached case; the attached case needs the same explicit skip.

## Error handling

| Situation | `error.code` | Exit |
|---|---|---|
| no `live_tv` sources configured and TBCPL off | `not_configured` | 1 |
| `--season`, `--episode` or `--download` with a live ref | `usage` | 1 |
| `episodes --ref <live>` | `usage` | 1 |
| `--category` names a category that does not exist | `no_results` | 2 |
| `--search` matched no channel | `no_results` | 2 |
| channel absent from the current playlists | `no_results` | 2 |
| every playlist source failed to load | `providers_failed` | 3 |
| configured player binary not installed | `player_unavailable` | 4 |

`not_configured` is the state that made a `find --type live` design unworkable.
It is a caller-fixable condition, so it takes `exitUsage` per that constant's
own definition ("an invalid configuration value", `cmd/agentjson.go:20-23`), but
it carries a distinct `code` so an agent can tell it apart from a malformed
invocation and advise the user to add a source.

`episodes --ref <live>` is exit 1, not 2: exit 2 means a lookup ran and found
nothing, whereas asking a live channel for episodes is a category error that no
lookup can resolve.

## Testing

A package var `agentLiveTV` in `cmd`, seamed exactly like `agentProvider` and
`agentSearch` (`cmd/find.go:18-22`), returning a `*provider.LiveTV` built over an
in-memory M3U fixture. No test touches the network, and none launches a player —
`agentResolveAndPlay` and `agentPlayerCheck` are already stubbed by the existing
suite, and `resolveLiveRef` gets the same treatment. Package-level vars and the
global `cfg` are restored with `t.Cleanup`.

Every test is watched to fail first, on an assertion rather than a compile error.

The load-bearing cases, each fed the input that would violate the guarantee it
claims — not a neighbouring one:

- **ID drift.** A ref captured from playlist A is replayed against a reordered
  playlist B in which its `ID` now denotes a *different* channel. The test
  asserts the correct channel is played, via `TVGID`. Feeding an empty playlist
  here would only prove the empty case and would let the real bug ship
  underneath a passing test.
- **Fail closed.** A ref for a channel absent from the current playlists exits 2,
  and `agentResolveAndPlay` is never called. The second half is the assertion
  that matters: it proves the live ref cannot leak into the title-search path.
- **Type gate.** `play --ref <live> --season 1 --episode 1` exits 1 without the
  provider being constructed, proving the branch precedes
  `validateSeasonEpisode` and its fabricated season list.
- **No `tvg-id`.** A playlist where no entry carries the attribute still
  round-trips `channels` → `play` by exact name.
- **`not_configured`.** Zero sources yields exit 1 with that code, not exit 2.
- **`episodes --ref <live>`** exits 1 and enumerates nothing.

`channels` is bounded by construction — one playlist fetch behind the existing
60s client timeout, no fan-out — so it satisfies the "agent-facing commands must
be bounded" rule without new deadline machinery.
