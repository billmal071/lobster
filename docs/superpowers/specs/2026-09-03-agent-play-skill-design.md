# Agent-driven playback: non-interactive CLI mode + `lobster-play` skill

Date: 2026-09-03
Status: approved design, not yet implemented

## Problem

A user should be able to tell a coding agent "find and play Parasite" and have
it work. Today that is impossible, and not for a small reason.

Every search path — the bare `lobster <query>` root command, `trending`, and
`recent` — routes selection through `internal/ui.Select`, which shells out to
`fzf` and blocks on human input (`internal/ui/ui.go:24-86`). It does this
unconditionally, including when the search returned exactly one result. The
existing `--json` flag does not help: it is read in `playStream`
(`cmd/search.go:470`), *after* a title has been chosen interactively, so
`lobster --json "the matrix"` hangs at the fzf prompt rather than printing
anything.

So an agent cannot complete the task, and worse, an agent that *tries* will hang
until killed — which the user experiences as the agent freezing, with no error
to explain it.

A skill file alone would document a workflow the CLI cannot perform. This design
covers both halves: a machine-readable CLI surface, and the skill that drives it.

## Scope

**v1: movies and TV episodes.** They share one code path and cover the common
case.

**Live TV is deferred to its own spec.** Not for effort reasons — it has a
correctness problem that needs a real answer first. `internal/provider/livetv.go:161-172`
disambiguates colliding channel IDs by *load order* (`base`, `base-2`,
`base-3`), and base IDs come from remote iptv-org playlists refetched per
process, where a failed source is silently skipped (`livetv.go:134-137`). So a
`channels` → `play --channel sports-2` round trip across two processes can
select a different channel, or none. There is also no `cmd/`-layer LiveTV
wiring at all today; it is constructed only in the TUI
(`internal/tui/app.go:145`).

**Channel surfing is out of scope permanently.** The continuous
next/previous flow has no terminal state, so there is nothing coherent for a
command to return. The skill hands off to the interactive CLI.

**An MCP server was considered and rejected** as surface area to maintain for no
capability the CLI cannot already provide.

## Command surface

Three commands. All emit JSON on stdout, none ever invoke `fzf`.

```
lobster find "the matrix" [--type movie|tv] [--limit N]
lobster episodes --ref <REF> [--season N]
lobster play --ref <REF> [--season N --episode N] [--detach]
```

Every existing command is untouched. Nothing about interactive use changes.

### Why new verbs rather than flags on existing commands

Adding `--pick N` to the root command was rejected: it makes one code path serve
both interactive and non-interactive selection, which is where selection bugs
hide, and it overloads `--json`, which today means "post-selection stream
metadata."

Namespacing as `lobster agent search|play` was rejected as clunky and
duplicative.

The one idea kept from the namespaced option is an explicit contract marker:
every payload carries `"schema": 1`, so a skill written against a different
lobster version detects the mismatch instead of silently misparsing.

## The replay reference (`--ref`)

This is the core of the design, and the first draft of this spec got it wrong.

**An ID alone is not sufficient to replay a selection.** Three facts force this:

1. `media.SearchResult.ID` is provider-scoped — `media/types.go:25` calls it
   "Provider-specific ID".
2. The resolver **re-searches by title** on every fallback provider:
   `resolveWithProvider` calls `p.Search(req.Title)` (`internal/resolver/probe.go:84`).
   Its doc comment (`probe.go:65-67`) states plainly that "IDs are not portable
   across all of them." ID equality is only a tiebreak in `candidateScore`,
   worth +8 of a possible 16 (`probe.go:198-227`).
3. `find` aggregates across providers. `gatherSearchResults` merges the
   primary's results with parallel fallback results whenever the primary
   returned fewer than three (`cmd/multisearch.go:36`), so one payload contains
   IDs from mixed namespaces. `cmd/multisearch.go:112-124` documents a real past
   bug where a donated ID from the wrong namespace caused playback of a
   different work.

Given only `--id`, `resolver.Request.Title` and `.Year` would be empty, so the
provider would be asked to `Search("")` and ranking would collapse. The result
is not a failure — it is *playing the wrong film*, which is exactly what
confirm-before-play was supposed to prevent.

**Therefore `find` emits an opaque `ref` per result**, a base64url-encoded JSON
object carrying everything replay needs:

```json
{"id":"movie/watch-the-matrix-19724","title":"The Matrix",
 "year":"1999","type":"movie","base":"flixhq.ws"}
```

`base` is included because the primary provider is flag/config-selected, and an
ID found under `--base yts` is meaningless under the default base.

The agent echoes a `ref` back verbatim and never constructs one. The token is
opaque by contract but plain base64 for debuggability — decoding it by hand
during support is deliberate.

**Honest cost:** this makes `lobster play` less pleasant for humans than a bare
`--id` would have been. Humans keep the existing interactive path, which is
unchanged. `--ref` is a machine contract and is documented as such.

**What `ref` does and does not guarantee.** It pins the *selection* — the title,
year, type and originating base the user confirmed. It does not pin the
*stream*: resolution still happens at play time through the fallback chain, and
if the originating provider is down, another provider's copy of the same title
may be served. That is lobster's existing and desirable behaviour. The
distinction is stated in the skill so the agent does not overclaim.

## Output contract

**stdout is always pure JSON. All human and diagnostic text goes to stderr.**
This holds for errors too, so an agent can parse unconditionally.

```json
{"schema":1,"results":[
  {"idx":0,"ref":"eyJpZCI6Im1v...","title":"The Matrix",
   "year":"1999","type":"movie"}
]}
```

`year` is a **string** and may be empty — `media.SearchResult.Year` is a string
(`media/types.go:28`) and is frequently absent; `cmd/multisearch.go` has
extensive year-less handling. A schema promising an int would either lie or
lose data.

`idx` is stable **within a payload only**. `multiProviderSearch` applies a 5s
timeout and discards slow providers (`cmd/multisearch.go:15,58`), so the result
set legitimately varies between runs. Agents must use `ref`, never a
remembered `idx`.

`--limit` interacts with a broadening heuristic worth knowing: fallback
providers are only queried when the primary returns fewer than three results
(`multisearch.go:36`), so a large limit usually returns primary-only results.

Errors use the same envelope:

```json
{"schema":1,"error":{"code":"no_results",
 "message":"nothing matched \"the matirx\""}}
```

### Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | success |
| 2 | no results |
| 3 | all providers failed |
| 4 | player unavailable |
| 1 | anything else |

`2` and `3` are deliberately distinct. On this repo `3` is common — the title
exists but every source is down — and should send the agent to `lobster doctor`,
not to a spelling suggestion.

**This requires plumbing that does not exist yet.** `cmd/root.go:44-48`
hard-codes `os.Exit(1)`, and neither `SilenceErrors` nor `SilenceUsage` is set
(`root.go:34-41`), so flag-parse and `loadConfig` failures print plain-text
`Error: …` plus full usage before any command body runs. Both must change for
the contract to hold. (Cobra already routes error/usage text to stderr, so that
part is satisfied.)

## Detached playback

`lobster play --detach` **re-executes lobster as a background supervisor.** The
foreground process spawns `lobster play --ref … --_supervised`, waits briefly to
confirm it is still alive, prints JSON, and exits.

The first draft specified detach as "skip `cmd.Wait()`". That was wrong, and
would have produced a black screen on lobster's most important paths, because
playback depends on process-local resources that die when lobster exits:

- **The de-obfuscating HLS proxy.** `wrapDeobfuscated` starts a loopback proxy
  and returns a cleanup that every player defers (`mpv.go:41`, `vlc.go:33`,
  `generic.go:31`). It serves `Deobfuscate` streams — set by AniPub
  (`internal/provider/anipub.go:207`), the working anime path.
- **The torrent server.** `cmd/search.go:518-522` stands up an in-process
  `torrentstream` server with `defer ts.Close()`. YTS returns magnets
  exclusively.
- **The subtitle temp dir.** `cmd/search.go:497-499` defers `os.RemoveAll`.

It was also not mechanically possible as described: `vlc.go:57` and
`generic.go:53` call `cmd.Run()`, with no `Start`/`Wait` to split.

Re-exec avoids all of it. The background process performs an ordinary attached
play, so proxy, torrent server, subtitles and mpv IPC all behave exactly as they
do interactively — including resume tracking, which is preserved rather than
sacrificed.

```json
{"schema":1,"status":"playing","pid":48213,
 "log":"~/.cache/lobster/play-48213.log","resume_tracking":true}
```

Implementation requirements:

- **Platform-specific spawn.** `syscall.SysProcAttr{Setpgid:true}` is Unix-only;
  Windows needs `CreationFlags: CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS`. CI
  runs windows-latest, so this needs a `detach_unix.go` / `detach_windows.go`
  pair, following the existing build-tag pattern (`ipc_windows.go`,
  `fzf_path_windows.go`). No `SysProcAttr` exists anywhere in the tree today.
- **Child output goes to the log file**, never to the inherited pipe — all three
  players set `cmd.Stdout = os.Stdout` (`mpv.go:80`, `vlc.go:53`,
  `generic.go:49`), which would otherwise interleave with the JSON already
  written and corrupt it. Child stdin is nil; there is no TTY.
- **Liveness wait before reporting success.** `cmd.Start()` succeeding only
  means the binary exec'd. mpv's own "stream failed to load" detection is
  "exited in under 5s with no playback" (`mpv.go:120-122`). The foreground waits
  ~1s and confirms the child is alive before emitting `status: playing`;
  otherwise it reports the failure with exit code 3 or 4 and points at the log.
  This does not eliminate the race — a stream can still die at 3s — but it
  removes the common case of reporting success for a stream that never started.
- **`resume_tracking` is player-dependent, not detach-dependent.** VLC and
  Generic never track position (`vlc.go:26-27`, `generic.go:25` both return an
  empty `PlayResult`). The field reflects the actual configured player.

## The skill

Ships in-repo at `skills/lobster-play/SKILL.md`, with install instructions in
the README (copy to `~/.claude/skills/`, or an `install.sh` flag). Keeping it in
the repo versions it alongside the CLI contract it depends on.

The frontmatter description triggers on intent, not tool name — users say "put
on Parasite," not "use lobster":

> Use when the user asks to find, search for, or play a movie or TV episode.

The body teaches five things:

1. **Run `find` first, show candidates, and stop.** Never play without
   confirmation. An instruction, not a judgement call left to the model.
2. **Never construct a `ref`** — only echo back one `find` returned.
3. **Always `--detach`**, and report the pid and log path.
4. **Do not overclaim.** `ref` pins the selection, not the stream; if a
   different provider serves the title, say so rather than asserting the exact
   source.
5. **A do-not-run list.** This is the highest-value part: without it the most
   likely agent failure is an indefinite hang with no error.

   | Command | What happens |
   | ------- | ------------ |
   | `lobster` (no args) | opens a full-screen Bubble Tea TUI (`cmd/search.go:41-52`) |
   | `lobster <query>` | blocks on fzf |
   | `lobster trending` / `recent` | blocks on fzf |
   | `lobster history` | blocks on fzf (`cmd/history.go:31`) |

Error handling maps the exit codes: `2` suggest a spelling fix, `3` run
`lobster doctor` and report which providers are down, `4` the player is not
installed.

## Testing

Red-green throughout, per existing repo practice.

**The primary guard is a hostile environment, not a mock.** Run each new command
in a test with `os.Stdin` replaced by a closed pipe *and* a `PATH` containing an
`fzf` shim that fails the test if executed. Injecting `ui.Select` alone —
the first draft's proposal — is insufficient, because it misses every other
blocking vector:

- `ui.Input` (`ui.go:98-127`) execs fzf directly and never touches `Select`;
  used at `search.go:290,364`.
- `ui.SelectWithTimeout` (`ui.go:133-196`) calls `term.MakeRaw(os.Stdin)` and
  blocks reading stdin before reaching `Select`; used at `session.go:94`.
- `tui.StartApp` (`search.go:52`) is Bubble Tea, not fzf at all.
- All three players set `cmd.Stdin = os.Stdin`.

The environment guard catches all of these, and any vector added later.
Injecting `ui.Select` remains a useful complement, not a substitute.

**A `newPlayer` seam is required**, mirroring the `flixhqDomain` stubbing
pattern already in `cmd/fallback_providers_test.go:30-36`. `player.New` is
called concretely at `cmd/search.go:571`; without a seam the detach test can only
be written as an integration test that really launches mpv.

Also covered:

- `find` → `play` round-trip **through the real fallback resolver**, not only a
  stub provider — that is where the ID-portability problem bites, so a stub
  would test the wrong thing.
- Every error path emits parseable JSON on stdout with the correct exit code.
- Nothing writes to stdout except the JSON envelope. (Spinners are already
  stderr-only — `internal/ui/spinner.go:17-25` — but player output is not.)
- `ref` encode/decode round-trips, and a malformed `ref` produces a clean error
  rather than a panic.

Tests make no live network calls, following the `TestMain` stubbing pattern in
`cmd/fallback_providers_test.go`, which exists precisely to stop tests probing
real hosts.

## Sequencing risk

This touches `cmd/fallback.go`, `cmd/search.go` and the provider chain — the
same area PR #36 (`feat/tbcpl-catalog-feed`) is resolving conflicts in.
Implementation should wait for #36 to land, or proceed on a clean worktree.

## Decisions taken

| Decision | Choice | Why |
| -------- | ------ | --- |
| Scope | Skill + non-interactive CLI | Skill alone documents an impossible workflow |
| Ambiguous matches | Always confirm with user | Removes the need for a ranking heuristic entirely |
| Media types | Movies + TV; Live TV deferred | Channel IDs are not stable across processes |
| Replay | Opaque `--ref` token, not `--id` | IDs are provider-scoped; resolver re-searches by title |
| Detach | Re-exec self as supervisor | Proxy, torrent server and subtitles must outlive the foreground |
| Surface | New verbs + `schema` field | Avoids dual-mode selection paths |

## Revision note

An adversarial review of the first draft found that `play --id` could play the
wrong film, and that `--detach` as specified would break anime and all YTS
playback. Both claims were verified against source before this rewrite. The
first draft's "there is no re-search between confirmation and playback" was
simply false. Recorded here because the same mistake is easy to repeat: lobster's
resilience comes from re-searching across providers, which is precisely what
makes a bare ID an unreliable handle.
