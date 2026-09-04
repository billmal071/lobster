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
| **4** | The configured player isn't installed or isn't on PATH |

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

# Preferred streaming server (Vidcloud, UpCloud)
provider = "Vidcloud"

# Subtitle language
subs_language = "english"
audio_language = "english"   # preferred audio track on multi-dub releases

# Video quality (360, 480, 720, 1080)
quality = "1080"

# Save watch history
history = true

# Auto-play next episode with countdown (true = countdown, false = menu only)
auto_next = true

# Download directory
download_dir = "~/Videos/lobster"

# Fall back to YTS torrents when every streaming provider fails.
# Off by default on purpose: YTS resolves to a magnet, so this makes lobster
# join a BitTorrent swarm, and your IP is visible to its peers. `--base yts`
# always works without this — the setting only controls automatic fallback.
# Torrent sources can be played but not downloaded with --download.
torrent_fallback = false
```

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
-c, --continue              Resume from watch history
-a, --audio-language <lang> Preferred audio track language (default: english)
-d, --download <path>       Download to path instead of streaming
-j, --json                  Output stream metadata as JSON
-l, --language <lang>       Subtitle language (default: english)
-n, --no-subs               Disable subtitles
-p, --provider <name>       Server: Vidcloud | UpCloud
-q, --quality <quality>     Video quality: 360 | 480 | 720 | 1080 | best
    --player <player>       Player: mpv | vlc | iina | celluloid
-x, --debug                 Debug logging to stderr
```

## Troubleshooting

**fzf not found**: Install fzf (`brew install fzf` / `apt install fzf`)

**No servers found**: The content may be unavailable. Try a different title or use `-p UpCloud` to switch servers.

**Subtitles not showing**: Check your player supports VTT subtitles. mpv handles this natively.

**Quality not changing**: Run with `-x` to see debug output. The `-q` flag selects the closest available HLS variant — if only one quality is offered by the server, that's what you get.

**libncursesw warnings with mpv**: Harmless library version mismatch. Does not affect playback.
