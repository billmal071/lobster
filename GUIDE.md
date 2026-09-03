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
