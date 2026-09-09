---
name: lobster-play
description: Use when the user asks to find, search for, or play a movie or TV episode — for example "put on Parasite", "find me something to watch", "play The Bear season 2 episode 3".
---

# Playing movies and TV with lobster

`lobster` is a terminal media streamer. It has a machine-readable mode that
never prompts, which is what you use. Its interactive commands will hang you
forever — see the do-not-run list below.

## Never run these

These block on `fzf` or open a full-screen TUI and will not return:

| Command | What happens |
| ------- | ------------ |
| `lobster` (no arguments) | opens a full-screen TUI |
| `lobster <query>` | blocks on an fzf picker |
| `lobster trending` / `lobster recent` | blocks on an fzf picker |
| `lobster history` | blocks on an fzf picker |

Use `find`, `episodes` and `play` instead. They print JSON on stdout and
never wait for input.

## The workflow

### 1. Find candidates

```sh
lobster find "the matrix" --limit 10
```

```json
{
  "schema": 1,
  "results": [
    {"idx": 0, "ref": "eyJpZCI6...", "title": "The Matrix", "year": "1999", "type": "movie"}
  ]
}
```

Add `--type tv` or `--type movie` when the user was specific ("play the
*series*"), so a same-named film and show do not both come back.

### 2. Show the user the candidates and stop

**Always ask which one before playing.** Do not pick for them, even when one
result looks obviously right. List the titles and years and wait for an answer.

### 3. Play the one they chose

```sh
lobster play --ref "eyJpZCI6..." --detach
```

```json
{"schema": 1, "status": "started", "pid": 48213,
 "title": "The Matrix",
 "log": "/home/u/.cache/lobster/play-847264193.log",
 "resume_tracking": true}
```

**`--detach` is mandatory for you.** Two separate reasons, either one fatal:

1. Without it the command blocks for the entire film and your tool call will
   not return.
2. Without it the JSON-on-stdout contract does not hold. An attached player
   inherits lobster's stdout, so mpv's progress output is interleaved with the
   JSON envelope and stdout will not parse. Only `--detach` redirects the
   player's output (to the `log` file) and leaves stdout clean.

`"status": "started"` means a player process was started — not that anything is
on screen yet. lobster returns after about a second, while finding a working
source and extracting the stream usually takes five to thirty, so most failures
happen after this response was sent. **If the user says nothing happened, read
the `log` path from this response** — that is where the child's output went.

Report the pid so the user can stop playback.

The `log` path is randomly generated (`play-<random>.log` in the user cache
dir) — it is not derived from the pid, so the only way to find a given run's
log is the `log` key in that run's own JSON output. `resume_tracking` reflects
whether the *configured player* reports playback position (true for mpv,
false for vlc/iina and other players) — it has nothing to do with whether you
detached; a detached play runs the same attached playback internally, in a
background process.

A ref remembers the provider base it was found under (e.g. `find --base
yts`). Both `play --ref` and `episodes --ref` resolve against that same base
automatically, so you normally don't need to pass `--base` yourself. Pass
`--base` explicitly only to deliberately override it.

## TV series

A TV ref needs both `--season` and `--episode`. Get the numbers first:

```sh
lobster episodes --ref "eyJpZCI6..." --season 2
```

```json
{"schema": 1, "title": "Some Show", "seasons": [1, 2, 3], "season": 2,
 "provider": "vaplayer", "episodes": [{"number": 1, "title": "Pilot"}]}
```

`provider` names the source that answered. It is often not the base the ref
carries: a ref that the configured provider cannot enumerate is re-searched
across the fallback chain, and the listing comes from whichever provider has
the show. Report it when a listing looks wrong — a season with an implausible
episode count is a provider problem, not a ref problem.

Then:

```sh
lobster play --ref "eyJpZCI6..." --season 2 --episode 3 --detach
```

## Rules

- **Never construct or edit a `ref`.** Only pass back one that `find` returned
  verbatim. Refs are opaque; their contents are not a stable interface.
- **Never reuse an `idx` from an earlier search.** It is only meaningful inside
  the payload it came from. Search results vary between runs.
- **Do not overclaim the source.** A ref pins the *selection*, not the stream.
  If the original provider is down, another provider's copy of the same title
  may be served. Say "playing The Matrix (1999)", not "playing it from X".
- **Check `schema`.** If it is not `1`, the installed lobster does not match
  this skill — tell the user instead of guessing at the output.
- **`--download` is not supported by `play`.** It is rejected outright; if the
  user wants a file saved rather than streamed, tell them to run the
  interactive CLI themselves.

## When something fails

Errors are JSON on stdout too, so parse unconditionally:

```json
{"schema": 1, "error": {"code": "no_results", "message": "nothing matched \"the matirx\""}}
```

Branch on the exit code. **Always read `error.code` too** — it tells you
*why* an exit happened, and the two commands below reuse exit 1 and exit 2 for
cases that are not "you called it wrong":

| Exit | Meaning | What to do |
| ---- | ------- | ---------- |
| 0 | success | — |
| 1 | bad invocation | You called it wrong: a malformed `ref`, a missing `--season`/`--episode`, `episodes` on a movie ref, `--season`/`--episode`/`--download` given for a live ref, an unrecognised `--type` or other flag, or an invalid config value. Also internal failures such as an unwritable cache directory. Fix the command; do not retry it unchanged. **Exception: `error.code: "not_configured"`** from `channels` or `play --ref` on a live ref means the user has no live TV sources set up at all — nothing was malformed. Tell them to add one under `[live_tv]` in the config; do not edit the command |
| 2 | no results | Suggest a spelling correction, or a different title. Also returned when the season or episode number does not exist — re-run `lobster episodes` and check. A live ref can hit exit 2 two ways: **`error.code: "no_results"`** means the channel is no longer in the playlist (playlists change upstream) — re-run `lobster channels` for a current ref. **`error.code: "ambiguous_channel"`** means the ref now matches more than one channel and lobster refused to guess. Two distinct playlist problems cause this: a duplicate tvg-id that survives Title narrowing, or (when the ref's tvg-id is empty) a duplicate folded channel name — Title narrowing only runs when the ref has a tvg-id, so an empty-tvg-id ref can't fall back to it. Either way this is a playlist data problem, not one a different ref value fixes: resolution never uses the ref's `ID`, so a fresh ref for either duplicate carries the same tvg-id/name/source and hits the same ambiguity again. Re-running `lobster channels` does not disambiguate — it only lets you inspect the conflicting rows. The playlist needs a unique tvg-id or name for each channel |
| 3 | every provider failed | Run `lobster doctor` and report which sources are down. Do **not** suggest a spelling fix — the title was found, the sources are broken. For a live ref this also covers the playlist it lives in failing to load on replay — the channel likely still exists, retry later |
| 4 | player unavailable | mpv (or the configured player) is not installed, or the background process could not be started |

**A misspelling exits 2, not 3.** `find` distinguishes "every provider answered
and none has this title" from "nothing answered at all", so exit 3 really does
mean broken sources — never reach for a spelling fix on it. It is not the
default failure; treat it as a genuine outage report and say so.

Exit 3 from `play --detach` is a narrower case: the background process was
started and then died within a second. The message names the log file. Read
that log rather than running `doctor` — the cause is in it.

## Live TV

`lobster channels` lists live TV channels as JSON. It never prompts.

With no flags it lists categories and how many channels each holds:

```sh
lobster channels
```

```json
{"schema": 1, "categories": [{"name": "News", "channels": 12}, {"name": "Sports", "channels": 40}]}
```

Categories come from the playlists' own group-title text. `Uncategorized` is
synthesised for channels whose playlist gives no group. A channel listed under
several groups is counted once per group, so the counts can sum to more than
the number of distinct channels — do not treat the sum as a channel total.

Pass `--category` (case-insensitive) or `--search` (substring match on name,
case-insensitive) to list matching channels instead, each with an opaque
`ref`:

```sh
lobster channels --category news --limit 10
lobster channels --search "bbc"
```

```json
{"schema": 1, "channels": [{"name": "BBC News", "category": "News", "logo": "https://...", "ref": "eyJpZCI6..."}]}
```

`--limit` caps how many channel rows come back (default: no limit) — it has
no effect on the categories view.

A partial listing can still be a success: if one playlist source fails to
load but others answer, `channels` returns exit 0 with whatever loaded, plus
a `failed_sources` array naming what didn't. Check for that key even on
success — a channel missing from the list may be a symptom of a listed
failure rather than genuinely absent.

### Playing a channel

Play a channel ref exactly like a film ref:

```sh
lobster play --ref "eyJpZCI6..." --detach
```

**`--detach` is more than recommended here — a live stream never ends**, so
without it the command never returns at all (a film only blocks for its
runtime; a channel blocks forever).

`--season`, `--episode`, and `--download` are all rejected outright if passed
with a live ref, regardless of value — a channel has no seasons and cannot be
downloaded.

A live ref is re-matched against the current playlists on every play, not
trusted blindly — playlists reload and reorder, so this is what lets a ref
stay valid across a reorder instead of silently playing whatever now sits at
the old position. It refuses to guess: if the match is ambiguous or the
channel is gone, `play` fails rather than picking one. See the exit-code
table above for `not_configured`, `no_results`, and `ambiguous_channel` on a
live ref.

**The detach log can contain the raw stream URL.** `channels` and `play`
never print a channel's stream URL themselves (an Xtream URL embeds the
subscriber's credentials in its *path*, which cannot be redacted the way a
playlist source's query string can), but on `--detach` the player's own
stdout/stderr — including whatever URL it prints when it opens the
stream — goes into the `play-*.log` file named by the `log` field. That field
is not exit-3-only: it is part of the success envelope too (`"status":
"started"`, since a detached parent reports success the moment the player
starts, well before playback or failure), so the path — and whatever
credentials it can lead you to — is available on the happy path as well as
the failure one. Read that log to diagnose a failure, but do not paste its
contents into shared output or a public issue without checking it first.
