# Live TV on the agent surface: `channels` + live refs

Date: 2026-09-05
Status: approved design, not yet implemented
Revision: 2 — first draft was reviewed by two verification agents; their
findings are folded in and noted at the end.

## Problem

The agent surface shipped in #43 covers movies and TV episodes and explicitly
deferred live TV to this spec. The deferral was not about effort. Two real
problems had to be answered first.

**1. Channel IDs are not stable across processes.** `doLoad`
(`internal/provider/livetv.go:127-159`) assigns each channel `p.uniqueID(ch.ID)`,
and `uniqueID` (`livetv.go:161-172`) resolves a collision by appending `-2`,
`-3` in playlist iteration order. That order comes from remote playlists
refetched per process, merged across `cfg.LiveTV.Sources()` plus the TBCPL feed
(`cmd/fallback.go:50-71`), where a failed source is silently skipped. So
`bbc-one-2` is not a name for a channel; it is a name for a position. A
`channels` → `play` round trip across two processes can select a different
channel than the one the user was shown.

**2. Channels claim to be movies.** `channelResult` (`livetv.go:188-190`) stamps
every channel `media.Movie`. `play`'s normal path re-searches by title across
the whole fallback chain (`resolveAndPlay`, `cmd/search.go`), so a ref for
"BBC One" would be handed to FlixHQ as a title query — and FlixHQ has
documentaries with that in the name. The failure is silent and it plays the
wrong thing.

A third concern from the first draft has been **withdrawn**. That draft claimed
`LiveTV.GetSeasons` — which fabricates `[]media.Season{{Number: 1, ID: id}}`
(`livetv.go:251`) — would cause `validateSeasonEpisode` to approve a live ref.
It would not: `playRun` only calls that gate when `sel.Type == media.TV`
(`cmd/play.go:199-205`), which a live ref never is. The fabricated season list
is still a reason a live ref must never be *typed* `tv`, but it is not a gate
this design has to defeat.

There is also no `cmd/`-layer LiveTV wiring at all: `provider.NewLiveTV` is
called once, in the TUI (`internal/tui/app.go:145`). `cmd/search.go:52` only
passes source strings into `tui.StartApp`, and `cmd/surf.go:90` drives the
provider the TUI built.

## Scope

**In:** browsing channels and playing one, non-interactively, from an agent.
Bounding the live load path, because the agent surface cannot use it otherwise.
Updating `skills/lobster-play/SKILL.md`, which currently tells the agent live TV
is unavailable.

**Out:** EPG / "what's on now". That needs playlist-header capture, an XMLTV
fetch of 10-50 MB, a parse, a cache, a `tvg-id` join and wall-clock now/next
queries — a subsystem, not a feature. `ParseM3U` currently discards the
`#EXTM3U` header carrying `url-tvg`/`x-tvg-url` (`internal/provider/m3u.go:42-43`),
so even the EPG URL is not retained today. It gets its own spec.

## Bounding the live load path

This is a prerequisite, not a nicety. CLAUDE.md requires agent-facing commands
to be bounded, and the current path is not:

- `doLoad` iterates sources **serially** (`livetv.go:132`);
- `fetch` retries the *entire* GET on the TLS-1.2 fallback client when the
  primary errors (`livetv.go:66-71`), and both clients carry `Timeout: 60s`
  (`livetv.go:51`);
- `liveTVSources()` appends every TBCPL live playlist (`cmd/fallback.go:50-71`),
  which is remote data with no cap.

Worst case is therefore *sources × 120s* with no context anywhere — about twelve
minutes for six sources. The first draft asserted this path was "bounded by
construction". It is not.

Three changes:

1. **`LoadContext(ctx context.Context) error`** becomes the real entry point.
   `load()` is retained as `LoadContext(context.Background())` so the TUI is
   unaffected.
2. **Sources are fetched in parallel**, bounded at 4 concurrent, results merged
   in *declared source order* so the merge stays deterministic regardless of
   completion order. Determinism matters: category ordering and the duplicate
   detection below must not vary run to run.
3. **The agent commands pass a deadline** of `liveLoadBudget = 15s`. The
   TLS-1.2 retry is attempted only if the remaining budget allows it. 15s rather
   than `find`'s 5s because a single playlist can legitimately be several MB;
   it is a named constant so it can be tuned in one place.

Partial loads are now first-class. `LoadContext` returns nil when *any* source
succeeded, and the provider records which sources failed. That record is what
lets the design tell "this channel is gone" from "the playlist it lives in did
not load" — an ambiguity the first draft papered over.

## Provider surface

`cmd/` cannot see any of what this design needs: every exported `LiveTV`
accessor returns `media.SearchResult`, and `channelResult` keeps only
`{ID, Title, Type, Poster}` (`livetv.go:188-190`). `p.channels` and `p.byID` are
unexported. So the provider gains:

```go
// Channel gains two fields. Both are additive; ID keeps its meaning.
type Channel struct {
    // …existing fields…
    TVGID  string // the tvg-id attribute, "" when absent
    Source string // the playlist this channel was loaded from
}

func (p *LiveTV) LoadContext(ctx context.Context) error
func (p *LiveTV) FailedSources() []string        // sources that did not load
func (p *LiveTV) AllChannels() []Channel         // in deterministic merge order
func (p *LiveTV) Lookup(key ChannelKey) []Channel
```

`TVGID` is set in `parseExtinf` (`m3u.go:56-72`), which today collapses the two
sources of identity into `ID` with no way to distinguish them afterwards:

```go
if id := attrs["tvg-id"]; id != "" {
    c.ID = id
} else {
    c.ID = slug(c.Name)
}
```

`Source` is set in `doLoad`, **not** in the parser. `ParseM3U(data []byte)`
never sees the playlist URL (`m3u.go:27`), but `doLoad` has `src` in hand and
currently discards it. The first draft called `Source` "advisory", which was
not a weakened guarantee — it was a field with no backing data.

## The live ref

`playRef` (`cmd/ref.go`) gains two fields, populated only for live refs:

```go
TVGID  string `json:"tvg_id,omitempty"`
Source string `json:"source,omitempty"`
```

`Type` is the string `"live"`, and `decodeRef` accepts it alongside `movie` and
`tv`.

`media.MediaType` is **not** extended. The live branch never constructs a
`media.SearchResult` for the resolver, so a `media.Live` constant would buy
nothing while rippling through the TUI and the resolver. `channelResult`'s
`media.Movie` stamp stays confined to the live provider and the TUI, where it
already lives and is load-bearing.

`searchResult()` (`cmd/ref.go:91-101`) is changed to return
`(media.SearchResult, error)` and to **error on a live type**. It has exactly
one non-test caller (`cmd/play.go:163`). This moves the safety property from
"the branch is in the right place" to "the converter refuses", so a future edit
that reorders `playRun` cannot silently reopen the hole.

### Matching: one key, fail closed at every branch

The ref's identity key is:

```
(TVGID if non-empty else folded Name) + Source
```

`resolveLiveRef` loads the playlists under the budget above, collects **every**
channel matching that key, and then:

| Matches | Outcome |
|---|---|
| exactly 1 | play it |
| 0, and `Source` loaded this run | `no_results`, exit 2 — the channel is gone |
| 0, and `Source` is in `FailedSources()` | `providers_failed`, exit 3 — naming the playlist |
| more than 1 | `ambiguous_channel`, exit 2 — listing the candidates |

There is no `ID` fast path: `ChannelKey` has no `ID` field. The rule as
implemented is tvg-id when present; when a tvg-id is shared by more than one
channel, narrow further by exact folded Title; Source narrows the result
throughout. `uniqueID`'s instability is harmless because resolution never
consults it at all, not because it is tried first and then overridden.

The ambiguous case is the first draft's worst omission. It assumed exact-name
matching was unambiguous; it is not, and neither is `tvg-id` matching — the very
existence of `uniqueID`'s collision suffixes is evidence that duplicate IDs are
routine in these playlists. **Ambiguity fails like absence.** Not first-wins,
which would reintroduce playlist iteration order as the tie-break — the exact
instability this design exists to escape — and not a ranking heuristic, which
would put guessing back into a design whose whole point is not guessing.

Including `Source` in the key shrinks the ambiguous set to "two channels with
the same name inside one playlist", which is rare but real, and which now
produces an honest error instead of a coin flip.

## Command surface

```
lobster channels
  → {"schema":1,"categories":[{"name":"Sports","channels":412}, …]}

lobster channels --category Sports [--limit N]
  → {"schema":1,"channels":[{"name":"BBC One","category":"Sports",
                             "logo":"https://…","ref":"…"}, …]}

lobster channels --search bbc [--category Sports] [--limit N]
  → same shape; --category and --search intersect

lobster play --ref <live-ref> [--detach]
  → the existing play envelope, unchanged
```

- **`--limit`** matches `find` (`cmd/find.go:14,97-99`), whose canonical call in
  SKILL.md uses it. Without it, `--category Sports` prints 412 rows each
  carrying a base64 ref. The two list commands must not disagree.
- **`--category` matches case-insensitively** and reports the playlist's
  canonical spelling in the output. Category keys are raw `group-title` text
  (`m3u.go:92-107`), so `News` and `news` are two buckets upstream; `find`
  already folds case on `--type` (`find.go:26-31`) and this should not differ.
- **`Uncategorized` is a synthesised bucket** (`m3u.go:94-96`), listed and
  selectable like any other, and documented as synthesised.
- **Category counts sum to more than the channel total.** A channel with
  `group-title="News;Sports"` appears in both. Documented, not deduplicated —
  the count answers "how many rows will `--category X` return".
- **`Args: cobra.NoArgs`.** `markAgentCommand` installs `ArbitraryArgs` when a
  command sets none (`cmd/agentjson.go:101-104`), so `lobster channels Sports`
  would silently ignore its argument.
- **Channel IDs are never printed.** The ref is the only handle, exactly as
  `find` does it — a caller that cannot see an ID cannot come to depend on one.

`channels` is registered with `markAgentCommand` so its pre-`RunE` failures emit
the envelope like the other three.

## Playback flow

`playRun`'s live branch is inserted immediately after `decodeRef`
(`cmd/play.go:158`), delegating to `playLiveRef` in `cmd/liveref.go`:

```
decodeRef → Type == "live"?
  ├─ yes → playLiveRef:
  │          reject --season/--episode        (exit 1)
  │          reject --download                (exit 1)
  │          agentPlayerCheck                 (exit 4)
  │          if flagDetach && !flagSupervised → playDetached(cmd, r); return
  │          resolveLiveRef → Channel
  │          LiveTV.Watch(ch.ID, "", "LiveTV", cfg.Quality) → media.Stream
  │          player.Play(stream, ch.Name, 0, nil)
  └─ no  → existing path, unchanged
```

Two details the first draft got wrong:

**The fork must happen before the playlist load.** `supervisorArgs` forwards
only the ref (`cmd/detach.go:86-96`), so the child reloads the playlists
regardless. If the parent resolved first, it would do the expensive work twice
*and* block before forking — breaking the "returns immediately" promise
SKILL.md makes for `--detach`. So `playLiveRef` dispatches to `playDetached`
before touching the provider, which is also why the player check comes first:
it is the one precondition worth reporting to the caller synchronously.

**The `!flagSupervised` guard must be reproduced** (`cmd/play.go:208`), or the
supervised child forks a child of its own and spawns without limit.

`playDetached` needs no changes: it is generic over the ref, and the child
re-enters `playRun` and takes this same branch.

### History

**No history is written for a live ref, and no skip is needed to achieve it.**
The first draft claimed the attached path required an explicit skip, gated by
`playerTracksPosition` (`cmd/detach.go:230`). That was wrong on both counts:
`playerTracksPosition` gates nothing — its only caller is `detachPayload`
(`detach.go:223`) filling an informational key — and history is written in
exactly two places, `playStream` (`cmd/search.go:598`) and `saveHistory`
(`cmd/session.go:351-366`), neither of which the live branch enters. It calls
`player.Play` directly, as `surf` does (`cmd/surf.go:88-94`).

`--continue` is likewise inert here: its resume lookup keys on
`e.ID == selected.ID` (`cmd/search.go:565-573`), and the live branch never
reaches that code. Worth stating explicitly, because live IDs are the unstable
positional ones and a future refactor that routed live through `playStream`
would resume from a stranger's position.

### Known limitation: detached log growth

`playDetached` sets `c.Stdout = c.Stderr = lf` (`detach.go:194-195`) and nothing
rotates `~/.cache/lobster/play-*.log`. A movie ends; a live channel left running
for days does not, so its log grows unbounded. Accepted for this change and
recorded here rather than silently inherited — mpv's output for a healthy
stream is low-volume, and capping it is a change to the detach path that affects
movies equally and belongs in its own commit.

## Error handling

| Situation | `error.code` | Exit |
|---|---|---|
| no `live_tv` sources configured and TBCPL off | `not_configured` | 1 |
| `--season`, `--episode` or `--download` with a live ref | `usage` | 1 |
| `episodes --ref <live>` | `not_a_series` | 1 |
| `--category` names a category that does not exist | `no_results` | 2 |
| `--search` matched no channel | `no_results` | 2 |
| channel absent, and its playlist loaded | `no_results` | 2 |
| more than one channel matches the ref | `ambiguous_channel` | 2 |
| channel absent, and its playlist failed to load | `providers_failed` | 3 |
| every playlist source failed | `providers_failed` | 3 |
| configured player binary not installed | `player_unavailable` | 4 |

`episodes --ref <live>` **needs no new code**: `cmd/episodes.go:41-43` already
returns `not_a_series` exit 1 for any non-TV ref, with the message
`"BBC One" is a live, which has no episodes`. Only a test pinning that
behaviour is in scope. The first draft's table wrongly specified `usage` here
and implied new code.

`not_configured` is the state that made a `find --type live` design unworkable:
`liveTVSources()` returns an empty slice when nothing is configured, and
`doLoad` treats zero sources as success-with-nothing rather than as `loadErr`,
so under `find` it would surface as exit 2 "nothing matched" — telling the agent
to rephrase a query when the fix is a config line. It takes `exitUsage` per that
constant's own definition, "an invalid configuration value"
(`cmd/agentjson.go:20-23`), but carries a distinct `code`.

**That requires a SKILL.md change to be usable.** SKILL.md:141 currently defines
exit 1 as "You called it wrong… Fix the command; do not retry it unchanged" and
never tells the agent to read `error.code`. As documented today, an agent
receiving `not_configured` would conclude it malformed its own command. The
skill must gain: a general instruction to read `error.code` on exit 1, and the
`not_configured` case by name, with the advice to tell the user to add a source
rather than to retry.

## Skill changes

`skills/lobster-play/SKILL.md` currently states under "Out of scope" that live
TV listing and surfing are unavailable and that the user should be sent to the
interactive CLI. That section is replaced by documentation of `channels` and of
live playback, plus the `error.code` guidance above. The first draft omitted the
skill entirely, which would have shipped a surface the agent is told not to use.

## Testing

A package var `agentLiveTV` in `cmd`, seamed exactly like `agentProvider` and
`agentSearch` (`cmd/find.go:18-22`), returning a `*provider.LiveTV`.

Fixtures are **M3U files under `t.TempDir()`**, reached through `fetch`'s
`os.ReadFile` branch (`livetv.go:74`) — `NewLiveTV` takes source strings and the
`client`/`httpDoer` fields are unexported, so this is the existing non-network
seam and no new network-capable one is needed. No test touches the network or
launches a player. Package-level vars and the global `cfg` are restored with
`t.Cleanup`.

Every test is watched to fail first, on an assertion rather than a compile
error. Each is fed the input that would violate the guarantee it names — not a
neighbouring one:

- **ID drift.** A ref captured from playlist A is replayed against a reordered
  playlist B in which its `ID` now denotes a *different* channel; the correct
  channel is played, via `TVGID`. An empty playlist here would only prove the
  empty case.
- **Ambiguity.** Two channels sharing a key produce `ambiguous_channel` exit 2
  and no playback — not a first-wins pick.
- **Fail closed.** A ref for an absent channel exits 2 and `agentResolveAndPlay`
  is never called. The second half is the assertion that matters: it proves the
  live ref cannot leak into the title-search path.
- **Absent vs. down.** The same absent channel, with its `Source` in
  `FailedSources()`, exits 3 instead of 2.
- **Converter refuses.** `searchResult()` on a live ref returns an error, with
  the live branch removed from `playRun` — this is what makes the safety
  property independent of branch ordering.
- **Type gate.** `play --ref <live> --season 1` exits 1 without the provider
  being constructed.
- **Detach ordering.** `play --ref <live> --detach` forks without loading any
  playlist, asserted by the provider seam never being called in the parent.
- **No `tvg-id`.** A playlist where no entry carries the attribute still
  round-trips `channels` → `play` by name.
- **Boundedness.** Sources that never respond do not exceed `liveLoadBudget`;
  asserted against a fixture whose reader blocks, with a fake clock or a short
  injected budget rather than a real 15s wait.
- **`not_configured`.** Zero sources yields exit 1 with that code, not exit 2.
- **`episodes --ref <live>`** exits 1 with `not_a_series`.

## Review record

Two verification agents reviewed revision 1 against the code. Of its
citations, 13 were correct, 6 had stale line ranges, and 2 were wrong (the
`NewLiveTV` construction site, and attributing history writes to
`cmd/history.go`, which only reads). The design review returned three blockers —
the false boundedness claim, the missing ambiguity rule, and `Source` having no
backing data — plus the history and detach-ordering errors corrected above. All
are addressed in this revision. The one claim that survived unchanged is the
central safety property: `decodeRef` has exactly two non-test callers, `episodes`
rejects non-TV refs before touching a provider, and the detached child re-enters
the same `playRun`, so no route was found by which a live ref reaches
`resolveAndPlay`.
