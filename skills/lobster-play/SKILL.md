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

```
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

### 2. Show the user the candidates and stop

**Always ask which one before playing.** Do not pick for them, even when one
result looks obviously right. List the titles and years and wait for an answer.

### 3. Play the one they chose

```
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

```
lobster episodes --ref "eyJpZCI6..." --season 2
```

```json
{"schema": 1, "title": "Some Show", "seasons": [1, 2, 3], "season": 2,
 "episodes": [{"number": 1, "title": "Pilot"}]}
```

Then:

```
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

Branch on the exit code:

| Exit | Meaning | What to do |
| ---- | ------- | ---------- |
| 0 | success | — |
| 1 | bad invocation | You called it wrong: a malformed `ref`, a missing `--season`/`--episode`, an unrecognised `--type`, `--download` (unsupported), or an invalid config value. Fix the command; do not retry it unchanged |
| 2 | no results | Suggest a spelling correction, or a different title. Also returned when the season or episode number does not exist — re-run `lobster episodes` and check |
| 3 | every provider failed | Run `lobster doctor` and report which sources are down. Do **not** suggest a spelling fix — the title was found, the sources are broken |
| 4 | player unavailable | mpv (or the configured player) is not installed |

Exit 3 is common. Providers break often; this usually means "try again later"
or "try a different title", not "you typed it wrong".

## Out of scope

Live TV channel listing and channel surfing are not available in this mode.
For those, tell the user to run `lobster` interactively themselves.
