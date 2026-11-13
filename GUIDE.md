# Lobster User Guide

## Quick Start

```bash
# Search and play
./lobster "breaking bad"

# Browse trending
./lobster trending

# Resume from history
./lobster history
```

## Searching

Pass your query as arguments or let lobster prompt you:

```bash
./lobster the bear             # search by name
./lobster                      # interactive search prompt
./lobster trending             # browse trending content
./lobster recent               # browse recently added
```

After searching, use fzf to select a result. Lobster shows metadata (rating, genre, synopsis) and asks for confirmation before playing.

## Continuous Playback (TV Shows)

When watching a TV show, lobster plays episodes continuously. After each episode finishes:

1. A **10-second countdown** starts: `Playing in 10s — press any key for menu, q to quit...`
2. If you do nothing, the **next episode auto-plays**
3. Press **any key** to open the navigation menu
4. Press **q** to quit immediately

### Navigation Menu

When you interrupt the countdown, you get these options:

| Option | What it does |
|--------|-------------|
| **Next episode** | Play the next episode |
| **Previous episode** | Go back one episode |
| **Replay current episode** | Watch the current episode again |
| **Episode list** | Pick any episode from the current season |
| **Quit** | Exit lobster |

### Cross-Season Navigation

- At the **last episode of a season**, "Next episode" jumps to the first episode of the next season
- At the **first episode of a season**, "Previous episode" goes to the last episode of the prior season
- At the **last episode of the last season**, lobster prints "End of series" and exits

### Disabling Auto-Play

If you prefer to always see the menu without a countdown, set `auto_next = false` in your config:

```toml
# ~/.config/lobster/config.toml
auto_next = false
```

## Quality Selection

Use `-q` to set your preferred video quality:

```bash
./lobster "inception" -q 720       # prefer 720p
./lobster "the bear" -q 1080      # prefer 1080p (default)
./lobster "anime" -q 480          # prefer 480p
```

Lobster parses the HLS master playlist and selects the variant closest to your preference. If your exact quality isn't available, it picks the closest one that doesn't exceed it.

## Subtitles

Subtitles are enabled by default, matched to your configured language.

```bash
./lobster "parasite" -l spanish   # Spanish subtitles
./lobster "movie" -n              # disable subtitles
```

## Downloading

Download instead of streaming:

```bash
./lobster "movie" -d ~/Videos              # download a movie
./lobster "show" -d ~/Videos               # download individual or batch episodes
```

When downloading TV episodes, lobster offers batch options:
- **Download all episodes** in the selected season
- **Download range** — e.g., `1-5`, `3,7,9`, `1-3,7,10-12`

## Watch History

Lobster saves your watch position. Resume where you left off:

```bash
./lobster history          # pick from watch history
./lobster "show" -c        # auto-resume from last position
```

## JSON Output

For scripting, get stream metadata as JSON:

```bash
./lobster "movie" -j | jq .url
```

Output format:
```json
{
  "title": "Movie Title",
  "url": "https://...",
  "quality": "1080",
  "subtitles": [...]
}
```

## Scripting and Agents

The commands above all use fzf, so they wait for a human. Three commands don't:
`find`, `episodes` and `play`. They never open a picker, never wait for input,
and print a JSON envelope on stdout — including when they fail.

### Finding something

```bash
./lobster find "the matrix"              # every match, as JSON
./lobster find "the bear" --type tv      # only series (movie | tv, case-insensitive)
./lobster find "dune" --limit 5          # cap the result count
```

```json
{
  "schema": 1,
  "results": [
    {"idx": 0, "ref": "eyJpZCI6...", "title": "The Matrix", "year": "1999", "type": "movie"}
  ]
}
```

The `ref` is the handle for everything else. It is opaque — don't build one or
edit one, just pass back what `find` printed. It carries the title, year and
type as well as the ID, plus the base it was found under, so `episodes` and
`play` resolve it against the same source without you passing `--base` again.
`idx` is only meaningful inside the payload it came from; results vary between
runs, so don't hold on to it.

### Listing episodes

```bash
./lobster episodes --ref "$REF"             # first season
./lobster episodes --ref "$REF" --season 2  # a specific one
```

```json
{
  "schema": 1,
  "title": "Some Show",
  "seasons": [1, 2, 3],
  "season": 2,
  "episodes": [{"number": 1, "title": "Pilot"}]
}
```

`seasons` is the full list, so one call tells you both what exists and what is
in the season you asked for.

### Playing

```bash
./lobster play --ref "$REF" --detach                          # a movie
./lobster play --ref "$REF" --season 2 --episode 3 --detach   # an episode
```

```json
{
  "schema": 1,
  "status": "started",
  "pid": 48213,
  "title": "The Matrix",
  "log": "/home/you/.cache/lobster/play-847264193.log",
  "resume_tracking": true
}
```

A series ref needs both `--season` and `--episode` — without them playback would
fall through to the interactive picker and hang, so `play` rejects it instead.

Pass `--detach` from a script. Attached, the player inherits lobster's stdout so
that a human running `play --ref` still sees mpv's output — which means the JSON
envelope is interleaved with progress lines and won't parse. `--detach` sends
the player's output to the `log` file and returns in about a second.

That second is the whole caveat: `"status": "started"` means a player process
exists, not that anything is on screen. Finding a working source usually takes
five to thirty seconds, so most failures land after the envelope was printed.
The `log` path is where they land, and it's randomly named rather than derived
from the pid, so keep it. `resume_tracking` says whether the configured player
reports playback position — only mpv does.

Attached, playing an episode starts the same continuous-playback session
described above: the countdown runs when it ends and the next episode follows.
Detached there is no terminal for that menu, so playback stops after the episode
you asked for.

`--download` is not supported by `play` — batch downloading is a prompt-driven
path. Use the interactive CLI for that.

### Exit codes

Errors are JSON on stdout too, so parse unconditionally:

```json
{"schema": 1, "error": {"code": "no_results", "message": "nothing matched \"the matirx\""}}
```

The exit code is what to branch on:

| Exit | Meaning |
|------|---------|
| **0** | Success |
| **1** | You called it wrong — bad ref, missing `--season`/`--episode`, unknown flag, invalid config value, `--download`. Also internal failures like an unwritable cache dir |
| **2** | Nothing matched. A typo, or a season/episode number the show doesn't have |
| **3** | Every provider failed. The title is fine, the sources aren't — run `./lobster doctor` |
| **4** | The configured player isn't installed or isn't on PATH, or the background process couldn't be started |

One exception to exit 3: from `play --detach` it means the background process
started and then died within a second. That isn't a provider outage report, and
`doctor` won't explain it — the error message names the log file that will.

Check `schema`. If it isn't `1`, the output shape has changed and your script
should say so rather than guess.

## Configuration

Config file: `~/.config/lobster/config.toml`

```toml
# Default player (mpv, vlc, iina, celluloid)
player = "mpv"

# Content source (default: "auto")
#
# "auto" means "no preference", and lets lobster pick per content type: a
# movie is played from YTS where YTS carries it, and a series always goes to
# a scraping source, because YTS has no TV catalogue at all. Series and
# anything YTS does not carry fall back to vaplayer, the general-purpose
# source "auto" maps to.
#
# Any other value is an explicit choice and is used for every title of either
# type — lobster never overrides it. Same for `--base` on the command line.
# Set it, and leave torrent_fallback false, if you would rather never join a
# torrent swarm:
#   base = "vaplayer"
# Available: auto, vaplayer, flixhq.ws, tbcpl, 1shows.org, animeonsen, yts,
# nyaa, eztv, 1337x (the last four are BitTorrent — see "Content sources")
# Retired (treated as "auto" with a warning here; an error as --base):
# soap2day, moviebox, vidnest, flixhq.to, kimcartoon, allanime, anipub.
# See "Content sources" below for what each one covers.
base = "auto"

# Preferred streaming server (Vidcloud, UpCloud)
provider = "Vidcloud"

# Subtitle language
subs_language = "english"

# External subtitle sources, used when the stream itself carries no track (or
# none in subs_language). SubDL ships with a key; OpenSubtitles needs your own,
# free from an opensubtitles.com account. They are searched in that order and
# the results are merged, so setting both is strictly better than one — the
# catalogues do not overlap, and a current-season title missing from one is
# often complete in the other.
# subdl_api_key = "..."
# opensubtitles_api_key = "..."

# Optional opensubtitles.com account, which only affects how many subtitle
# files you may download per day: the API key alone allows 5, the key plus
# these allows 20. Twelve episodes is more than 5, and running out looks like
# an HTTP 403 rather than a quota message, which is the only reason this is
# worth a password sitting in a config file. Leave both unset to stay on the
# 5/day tier — that is the default, and a login that fails falls back to it
# rather than breaking subtitles.
# opensubtitles_username = "..."
# opensubtitles_password = "..."
audio_language = "english"   # preferred audio track on multi-dub releases

# Video quality (360, 480, 720, 1080)
quality = "1080"

# Save watch history
history = true

# Auto-play next episode with countdown (true = countdown, false = menu only)
auto_next = true

# Download directory
download_dir = "~/Videos/lobster"

# Where a streaming torrent's pieces are written while it plays. This is not
# the same thing as download_dir: download_dir is a library of finished files
# you keep, while this holds a partial payload that is deleted when playback
# ends. Unset — the default — means ~/Videos/.lobster/torrent-<random>. See
# "Where torrent data lands".
# torrent_dir = "/mnt/scratch/lobster"

# Fall back to torrents (EZTV for series, Nyaa for anime, YTS for films, 1337x
# for either) when every streaming provider fails.
#
# This is NOT the swarm opt-in, and leaving it false does not keep you out of
# a swarm: the default `base = "auto"` already plays movies from YTS, so a
# default install joins one for films. To never join a swarm, choose an
# explicit non-torrent source instead — `base = "vaplayer"` — and leave this
# false. `yts`, `nyaa`, `eztv` and `1337x` are swarms even with this false.
#
# What this setting controls is the other direction: letting a *failed* stream
# resolution end up on a torrent — for a series, for a film YTS does not
# carry, and under a base you chose explicitly. Off by default because a swarm
# reached by choosing "auto" is documented, while one reached because a
# scraper broke is not. `--base yts`, `--base eztv`, `--base nyaa` and `--base 1337x`
# always work without this.
#
# Torrent sources can be downloaded as well as played: lobster serves the
# torrent over loopback and --download fetches from there. Note that
# downloading this way still joins the swarm, so your IP is visible to its
# peers for the whole download.
#
# Two paths refuse a torrent outright. The TUI's download queue is one (it
# also passes over MPEG-DASH and de-obfuscated HLS sources for the next
# one; `-d` handles both). The other is --json: a magnet is not a URL a
# JSON consumer can open, and the loopback URL would die with the process
# that printed it, so the run fails with "resolved to a torrent, which --json
# cannot express as a playable URL".
# Only session playback (a series, or an episode picked in the TUI) tries an
# HTTP source first and fails only when none can serve the title. A single
# title resolved directly, such as `--base yts` with `play --ref --json`,
# refuses the magnet without trying another source. Under `base = "auto"` a
# film is not routed to YTS when --json is set.
# Either way the `play --json` error keeps code providers_failed (exit 3), its
# message is the refusal text, and it carries "torrent_refused": true, plus
# error.providers when streaming sources were tried first.
# Play it without --json, or fetch it first with the interactive CLI, since
# `play` has no --download. The message names the source that fits the title:
# `lobster --base yts "title" --download <dir>` for a film; for an episode,
# `--base eztv` the same way, then pick the season and episode at its prompts,
# or `--base nyaa` for anime, where a sequel is its own "Title Season N" entry.
torrent_fallback = false
```

### Content sources

`base` (and `--base`) names the *primary* source: the one searched first, the
one asked to enumerate a series' seasons and episodes, and the starting point
for stream resolution. It does not pin the stream — every playback path still
falls back to the rest of the chain when the primary cannot serve a title, so
for **playback** naming a source that does not carry something is not fatal,
just slower.

`lobster episodes` is not covered by that. When the primary cannot enumerate a
ref's seasons it re-searches the chain by title, but a fallback's result is
only accepted if its title matches the ref's after normalisation. A show the
two spell differently — `Marvel's Agents of S.H.I.E.L.D.` in the ref against
`Agents of S.H.I.E.L.D.` on the fallback — is rejected, and `episodes` exits 2
with `no seasons found`. `play --ref` has no such check, so it plays the very
ref `episodes --ref` cannot list. If `episodes` says a ref has no seasons, run
`lobster find --base tbcpl` for the title and pass the new TBCPL ref to
`episodes`; the old ref's ID belongs to the source that found it, and TBCPL
cannot use it.

This table is about **scope** — what a source covers and what it structurally
cannot do. It says nothing about whether a site is up today; that changes week
to week, and `lobster doctor` is the live answer ("Check which providers work,
and where the others break"). Run it before concluding a source is broken.

| `base` | Covers | Worth knowing |
| --- | --- | --- |
| `auto` (default) | Films and series | Automatic routing: you name no source, so lobster picks one per content type. `vaplayer` is the general source `auto` maps to, and the primary every `auto` run searches with. A film is then looked up on YTS by title and year and played from there when both agree. A series is not — YTS is never even queried for one, because it has no TV catalogue — so it stays on `vaplayer`, and if `vaplayer` cannot enumerate its seasons, playback falls back to the chain, which re-searches every source by title. Films YTS has no match for stay on `vaplayer` too. The YTS lookup covers ordinary playback only: `--download` and `--json` runs stay on `vaplayer`, the first so a download you did not ask to make over BitTorrent does not silently join a swarm, the second because a magnet is not a URL a JSON consumer can open. Asking for it outright still works — `--base yts --download <dir>` downloads from YTS perfectly well. An explicit `base`, `--base`, or an `api_url` overrides all of this. |
| `vaplayer` | Films and series | General-purpose, API-based. The general source `auto` maps to, and the first source the fallback chain asks. |
| `flixhq.ws` | Films and series | Scraper-based; the default before `auto`. Checks its domain at startup and tries known alternates (plus any `domain_overrides`) when it is unreachable. Not to be confused with `flixhq.to`, a different and now retired provider. |
| `tbcpl`, `1shows.org` | Films and series | The same provider against the same site: `tbcpl` resolves to `https://www.1shows.org`, `1shows.org` to `https://1shows.org`. Honours `audio_language` for multi-dub releases. |
| `animeonsen` | Anime | Public API, no key and no DRM, and the only anime source in the automatic fallback chain, except that `torrent_fallback` adds Nyaa after it. Two things to know before naming it. It **carries no subtitles** — the stream is Japanese audio and the source has no text track at all, so you need `subdl_api_key` or `opensubtitles_api_key` set for anything to appear. And it has **one 720p rendition**, so `quality` has nothing to choose: a run under it reports 720 whatever you asked for, rather than claiming a height the stream does not have. Its episode counts are measured by probing the CDN (the catalogue endpoints need a key lobster does not ship), so a series with a gap in its numbering lists short rather than long. |
| `yts` | Films only | No TV catalogue at all, so a series named under `--base yts` is played from the fallback chain instead — for playback, `--base yts` pins nothing for a series. `lobster episodes` under it can still fail outright, per the note above. Resolves to a **magnet**, so playback joins a BitTorrent swarm and your IP is visible to its peers; lobster serves it over loopback, so `--download` works too, but the swarm is joined either way. If no peer answers within 90 seconds the run gives up with "the swarm may be dead". |
| `eztv` | Series only | TV series torrents, keyed by IMDb id: a title is looked up on TMDB and its IMDb id read from there, so a show TMDB does not know is not found. Seasons and episodes are real (episode IDs are `<season>x<episode>`), and `--quality` and `-q best` pick among the releases on offer; when nothing is at or below the requested quality, the best available release is picked rather than failing. lobster pages through a show's whole release list, stopping at an empty or short page or once EZTV's reported total is reached, but never past 100 pages (10,000 releases) or 20 seconds. When a page fails or either limit cuts the walk short, the season and episode lists come back with what was found together with an incomplete-list error and are not treated as complete (`episodes` adds a `season_list_incomplete` or `episode_list_incomplete` warning), so a season or episode missing from them is not proof it does not exist. The same holds when EZTV is a fallback whose incomplete list was one of those consulted: a season absent from every list, with any of them incomplete, makes `episodes --season N` exit 3 (`providers_failed`) rather than 2, and `play` goes on to ask the fallback chain for it. Resolves to a **magnet** like `yts`: playback joins a swarm and your IP is visible to its peers. Never reached automatically — a series stays on the scraping sources unless you name this base, or set `torrent_fallback`, which tries it only after every streaming source has failed. |
| `nyaa` | Anime only | Anime torrents from the English-translated category. Releases there can be dubbed and carry no usable subtitle track, so you may still need `subdl_api_key` or `opensubtitles_api_key`; playback continues without subtitles when none turn up. Nyaa has no notion of a series: the feed only exposes the **75 newest torrents per query**, so older episodes of a long-running show may be missing, and an entry opened later re-searches by the bare title, so a narrower query's older hits can drop out of its episode list, and a sequel season appears as its own entry ("Title Season N") rather than under the first. A name whose only number is a trailing year (`Show 2010`) is read as a release year, not episode 2010, and a name whose only numbers are a year span (`Show 1990-2010`) is read as a pack; both are skipped. One season per entry; `--quality` and `-q best` pick among the release groups' resolutions; when nothing is at or below the requested quality, the best available release is picked rather than failing. Resolves to a **magnet** like `yts`, with the same swarm and IP exposure, and is never reached automatically: name it, or set `torrent_fallback`, which tries it only after every streaming source, `animeonsen` included, has failed. |
| `1337x` | Films and series | Torrents scraped from 1337x's Movies and TV category searches, with the info hash read from each release's page and the magnet rebuilt from it with lobster's own trackers. Only releases whose name starts with the title you searched for are kept, and the title must be followed by a release year, a season or episode marker, or a quality or source tag (anything after a year is ignored), so a near-miss such as "Lost in Space" is not offered for "Lost". When comparing, accents and punctuation are ignored and `&` is read as "and", so "Law & Order" matches "Law and Order" but not "Law Order"; the end of the name and "Season N" are also accepted right after the title. When 1337x is reached as a fallback, the title searched for is the one the primary source gave, so a differently named release may not be found. A series search gives one entry for the query you typed, not one per title: type the title (and `S02E05` for an episode) as precisely as you can. A film search gives one entry per release year found in the release names when there is more than one. Seasons and episodes are read from the `SxxEyy` in release names, so a series only lists what the first three result pages of each category carry. For the episode or film asked for, the best-seeded release of each quality is offered, and `--quality` and `-q best` pick among them; when nothing is at or below the requested quality, the best available is used. Several mirrors are tried in turn (`1337x.to`, `1337xx.to`, `x1337x.eu`); a mirror that is down or answers with a challenge or block page is skipped. lobster does not solve challenges, so when every mirror challenges, the source fails. Resolves to a **magnet** like `yts`: playback joins a swarm and your IP is visible to its peers. Never reached automatically — only when you name this base, or under `torrent_fallback`, which races it with the other torrent sources, and only after every streaming source has failed. |

**Retired sources.** `soap2day`, `moviebox`, `vidnest`, `flixhq.to`,
`kimcartoon`, `allanime` and `anipub` were each verified dead upstream on
2026-11-07 with nothing lobster could fix, and are no longer wired in. Naming
one with `--base` is rejected with a message saying which source and why, so
an old script fails loudly instead of quietly searching somewhere else. One
left in the config file is treated as `auto` with a one-line warning naming
it, so no command (least of all `lobster doctor`, which lists what still
works) is locked out by a stale file; a ref stamped with one plays on the
configured source with a one-line warning, since `play` re-searches by title
anyway.

**Any other value lobster does not recognise is not an error.** Values are
matched by substring, so anything still containing a known name works —
`flixhq.ws.xx` is read as `flixhq.ws`. Anything else falls through to
`vaplayer`, the same source `auto` maps to: `--base sopa2day` searches
VaPlayer, and `find` reports it under a `base_unknown` warning.

`api_url` is the one way to name a source that does not go through `base` at
all: when it is set it replaces `base` entirely, and the value of `base` is
ignored.

### Changing source loses your resume positions

Watch history is keyed on the provider's own ID, and IDs are not portable
between providers. Changing `base` — including the upgrade that made `auto`
the default, which moved the primary from `flixhq.ws` to `soap2day`, and the
retirement of `soap2day` that moved it on to `vaplayer` — means
in-progress titles are looked up under an ID that is not in your history:
they restart from zero, and finishing one adds a second row for the same
title rather than updating the first.

The old rows are not deleted — they stay in `history.tsv` and are still listed
by `lobster history` — but they are no longer reachable. `lobster history`
re-searches the *current* primary and plays the row whose ID matches the saved
one; under a different `base` no result carries that ID, so it drops you into
the picker for that title and playback starts from the beginning.

To keep existing positions on the source you were using before, pin it:

```toml
base = "flixhq.ws"
```

Resuming *across* sources needs the history file to identify a title by
something portable rather than by provider ID, which is a change of its own.

### Where torrent data lands

A streaming torrent writes its pieces to disk as they arrive, and a film can be
tens of gigabytes — a 4K remux commonly 15-50GB. By default they go to
`~/Videos/.lobster/torrent-<random>`, which is on the same volume as your home
directory. That is deliberate: `/tmp` is frequently a small partition or a
tmpfs sized from RAM, so a single large torrent there can fill the root
filesystem outright, and `/tmp` is also private to a snap-confined process, so
a path written there is not reachable from outside it. (It is the same reason
subtitle staging lives under `~/Videos/.lobster/`.)

Set `torrent_dir` to put the data somewhere else — another drive, say. Whatever
the location, each run gets its own `torrent-<random>` subdirectory inside it
and nothing else in that directory is touched.

The directory is removed when playback ends. The payload is not kept for a
later resume: lobster stores no torrent state, so nothing could resume it, and
a kept directory would instead grow by a film's worth of data per run with
nothing deleting it. A run killed outright — power cut, `kill -9` — cannot
clean up after itself, so a later run sweeps any `torrent-*` directory that has
not been written to for 24 hours.

### Torrent storage backend

The torrent library stores pieces through one of two file backends. The default
memory-maps them, which is faster but has a race: a file can be truncated while
another mapping of it is still live, and reading the truncated tail raises
`SIGBUS` — a signal, not a Go error, so lobster dies mid-playback with no
recoverable failure.

Lobster avoids this for you. When a playback command could open a magnet — the
default `base = "auto"` (which plays movies from YTS), any `base` naming YTS
(`yts`, `yts.mx`, …), or `torrent_fallback = true` — it restarts itself once at
startup with the safer backend selected:

Under `auto` the restart follows the route, so the cases that suppress the YTS
lookup suppress the restart too: `--json`, `--download`, and a configured
`api_url` (which overrides `base` entirely) all stay on the default backend,
because none of them can reach a magnet. `torrent_fallback = true` is
independent of all three — it puts YTS in the fallback chain whatever source
you named — so it always restarts.

```sh
TORRENT_STORAGE_DEFAULT_FILE_IO=classic
```

The library reads that variable in its own package initialisation, before any
lobster code runs, so it can only be chosen before the process starts; that is
why lobster restarts rather than setting it in place. The restart replaces the
process (same pid, terminal and exit status) and happens before any search or
output, so it is not observable in normal use.

Two things worth knowing:

- **Setting it yourself is respected**, including `mmap`. If you would rather
  have the throughput and accept the race, set it and lobster leaves it alone.
- **On Windows there is no way to restart in place**, so lobster prints a
  warning instead. Set the variable in your environment before launching to get
  the safe backend there.
- **Commands that cannot play do not restart.** `version`, `find`, `episodes`,
  `doctor` and `channels` never reach playback, so they neither restart nor
  print the Windows warning.
- **A ref can name a torrent source too late.** `play --ref` adopts the base
  the ref was found under, and that happens after the backend has been chosen,
  so a ref minted under `--base yts` played from a config with an explicit
  non-torrent base streams on whichever backend that run was given. Restarting
  at that point would replay the command, so lobster prints the same warning
  instead. Pass `--base yts` explicitly, or set the variable, to get the safe
  backend for those runs.

Set it to anything other than `classic` or `mmap` and the library panics during
startup, before lobster can report it — the message will be a bare Go panic
naming the value you typed.

### TBCPL catalog feed

Lobster can pull site metadata from [tbcpl.lol](https://tbcpl.lol), a directory of streaming sites, to keep mirror domains fresh, add a best-effort fallback embed provider, and feed additional live-TV/sports channels. The catalog is cached for 12 hours with an embedded offline snapshot as a fallback.

```toml
# Participate in the TBCPL catalog feed (default: true)
tbcpl_feed = true

# Country overlay to add on top of the global list (default: "", global only)
# Valid values: BRAZIL, EGYPT, FINLAND, FRANCE, GERMANY, INDIA, ITALY, JAPAN,
# KURDISTAN, NETHERLANDS, POLAND, PORTUGAL, RUSSIA, SOUTHKOREA, SPAIN
tbcpl_region = ""

# Let sites not flagged "trusted" also participate in the generic-embed
# fallback race and live-TV feed (default: false)
tbcpl_include_untrusted = false
```

- `tbcpl_feed` — master switch for the feature. When `false`, lobster skips fetching the catalog entirely and none of the TBCPL-derived behavior (mirror refresh, fallback embeds, live-TV feed) is active.
- `tbcpl_region` — set to one of the values above to overlay that country's sites on top of the global list. Leave blank for the global list only.
- `tbcpl_include_untrusted` — by default only sites TBCPL flags as "trusted" participate in the generic TMDB-id embed fallback and the live-TV/IPTV feed. Set to `true` to widen that pool to all listed sites (lower confidence, more coverage).

## All Flags

```
-c, --continue[=false]      Resume from watch history (on by default)
-a, --audio-language <lang> Preferred audio track language (default: english)
-d, --download <path>       Download to path instead of streaming
-j, --json                  Output stream metadata as JSON
-l, --language <lang>       Subtitle language (default: english)
-n, --no-subs               Disable subtitles
-p, --provider <name>       Server: Vidcloud | UpCloud
-q, --quality <quality>     Video quality: 360 | 480 | 720 | 1080 | best
    --player <player>       Player: mpv | vlc | iina | celluloid
-x, --debug                 Debug logging to stderr
    --base <source>         Content source (default: auto — YTS for movies,
                            a scraping source for series). An explicit value
                            is used for both types; nyaa (anime), eztv
                            (series) and 1337x are torrent sources only ever used by
                            name or under torrent_fallback. See "Content
                            sources" above for what each value covers.
```

## Troubleshooting

**fzf not found**: Install fzf (`brew install fzf` / `apt install fzf`)

**No servers found**: The content may be unavailable. Try a different title or use `-p UpCloud` to switch servers.

**Subtitles not showing**: Check your player supports VTT subtitles. mpv handles this natively.

**Quality not changing**: Run with `-x` to see debug output. The `-q` flag selects the closest available HLS variant — if only one quality is offered by the server, that's what you get.

**libncursesw warnings with mpv**: Harmless library version mismatch. Does not affect playback.
