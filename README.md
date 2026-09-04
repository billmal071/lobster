# Lobster — Terminal Media Streamer

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)
![Cobra](https://img.shields.io/badge/Cobra-CLI-blue?style=flat)
![fzf](https://img.shields.io/badge/fzf-TUI-hotpink?style=flat)
![mpv](https://img.shields.io/badge/mpv-default%20player-6f4e7c?style=flat)
![VLC](https://img.shields.io/badge/VLC-supported-orange?style=flat)
![IINA](https://img.shields.io/badge/IINA-supported-lightgrey?style=flat)
![ffmpeg](https://img.shields.io/badge/ffmpeg-download-red?style=flat)
![Security](https://img.shields.io/badge/security-hardened-green?style=flat)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey?style=flat)
![License](https://img.shields.io/badge/license-GPL--2.0-blue?style=flat)

> **Search in your terminal. Stream instantly in your media player.**

Lobster is a security-hardened Go rewrite of [lobster.sh](https://github.com/justchokingaround/lobster). The original was a shell script; this version replaces every unsafe `eval` and shell interpolation with structured, auditable Go code.

---

## Features

- 🔍 Search movies and TV shows by name
- 🔥 Browse trending and recently added content
- 🎬 Stream in mpv, vlc, iina, or celluloid
- 🔄 Continuous playback — auto-plays next episode with 10s countdown
- 🧭 Episode navigation — next, previous, replay, episode list, cross-season
- ⬇ Download with ffmpeg for offline viewing
- 🌍 Subtitles with automatic language matching
- ▶ Watch history with resume support (`--continue`)
- 🎞 Quality selection — 360p, 480p, 720p, 1080p (HLS variant matching)
- 📺 Live TV — free IPTV channels including a 456-channel Sports category ([jump to guide](#live-tv-and-sports))
- 📦 JSON output mode for scripting and piping
- 🛰 TBCPL catalog feed — keeps mirror domains fresh and broadens source/live-TV coverage

See [GUIDE.md](GUIDE.md) for detailed usage instructions.

---

## Requirements

1. **Go 1.22+** — build only
2. **fzf** — runtime (interactive menus)
3. **mpv** — default playback
4. **vlc** — alternative playback
5. **iina** — macOS playback
6. **ffmpeg** — required for `--download`

## Installation

### Quick Install (Linux & macOS)

You can easily install Lobster system-wide by running our automatic installation script. It automatically detects your OS and architecture, and downloads the appropriate system package (`.deb`, `.rpm`, or `.tar.gz`) to install dependencies like `fzf`.

```bash
curl -sSfL https://raw.githubusercontent.com/billmal071/lobster/main/install.sh | sh
```

### From Source (macOS / Linux)

```bash
brew install go fzf mpv

git clone https://github.com/billmal071/lobster && cd lobster

go build -o lobster .
sudo make install # installs to /usr/local/bin
```

### Quick Install (Windows)

Run this in PowerShell — it downloads lobster and installs dependencies (mpv, fzf, ffmpeg) automatically:

```powershell
irm https://raw.githubusercontent.com/billmal071/lobster/main/install.ps1 | iex
```

Or download the zip from [Releases](https://github.com/billmal071/lobster/releases/latest), extract, and double-click `install.bat`.

### From Source (Windows)

Install [Go](https://go.dev/dl/), [fzf](https://github.com/junegunn/fzf#windows), [mpv](https://mpv.io/installation/) (or [VLC](https://www.videolan.org/)), and optionally [ffmpeg](https://ffmpeg.org/download.html). Ensure they are in your PATH.

```powershell
git clone https://github.com/billmal071/lobster && cd lobster

go build -o lobster.exe .
```

Config is stored in `%APPDATA%\lobster\config.toml` and history in `%LOCALAPPDATA%\lobster\history.tsv`.

---

## Usage

```bash
./lobster trending
./lobster 28 Years Later: The Bone Temple
./lobster Star Wars -q 1080
./lobster version
```

---

## Flags

```
-c, --continue
-a, --audio-language <lang>
-d, --download <path>
-j, --json
-l, --language <lang>
-n, --no-subs
-p, --provider <name>
-q, --quality <360|480|720|1080|best>
    --player <mpv|vlc|iina|celluloid>
-x, --debug
```

---

## Checking provider health

Providers break constantly — a domain moves, a player is replaced, an API starts
signing its requests. `lobster doctor` probes each one and names the stage that
broke, which is the difference between a cheap fix and a rewrite:

```
$ lobster doctor
Provider health (query: "The Matrix")

  ok   AniPub         2.912s           26 results, stream resolved
  ok   FlixHQWS       3.815s           4 results, 3 servers, embed via Vidmoly
  FAIL FlixHQ        20.514s  search   unexpected status 522
  FAIL MovieBox        569ms  search   unexpected status 440
  FAIL Soap2Day       1.848s  watch    no embed ID in video URL

4 of 11 providers usable.
```

A failure at `search` usually means a moved domain or a renamed field. A failure
at `watch` or `embed` means the player changed. Pass a title to probe with
something else; anime providers are probed with an anime title automatically.
Exits non-zero when nothing is usable, so it works as a check.
## Live TV and Sports

Live TV streams free, public IPTV playlists — by default the community-maintained
[iptv-org](https://github.com/iptv-org/iptv) index. No account and no API key.

### Watching football

Live TV lives in the dashboard, so start it with **no search query**:

```bash
./lobster
```

Then:

1. Press **`5`** to open the **Live TV** tab.
2. Pick the **Sports** category and press **Enter**. (It currently carries **456 channels**.)
3. Pick a channel and press **Enter** — it opens in your player.

After a channel starts, you get a **Next channel / Previous channel / Back to browser / Quit**
menu, so you can flip through the Sports lineup looking for the match without going
back to the list each time. On **mpv** the dead channels are skipped automatically;
on other players use the menu, and lobster tells you so on startup.

### Finding a specific match faster

456 channels is a lot to scroll. Press **`s`** or **`/`** to search channel names
directly — searching is a plain case-insensitive substring match on the channel
name, so search by **broadcaster, not by fixture**:

```
bein        → beIN SPORTS XTRA, beIN Sports USA, …
sky sport   → Sky Sport channels
espn        → ESPN feeds
dazn        → DAZN Combat, DAZN Darts, …
football    → CCTV-Storm Football, …
sport       → the broadest net
```

Searching `arsenal vs chelsea` will find nothing — there is no fixture metadata
in a playlist, only channel names.

### What to expect

These are free public streams, so a few things are worth knowing before kickoff:

- **Channel names carry annotations.** `[Geo-blocked]` means it will fail unless
  you are in the right country; `[Not 24/7]` means it only broadcasts part of the
  day. `beIN Sports USA (1080p) [Geo-blocked]` is a typical Sports entry.
- **Dead channels are normal.** Public IPTV links rot. mpv auto-skips them; after
  12 consecutive failures lobster stops so a fully-unreachable network can't loop.
- **Live channels can't be downloaded.** `--download` is rejected for Live TV.
- **First load is slow.** The category playlist is ~3 MB and is fetched once, then
  cached for the session.

### Adding your own playlists

If a match isn't on a free channel, point lobster at your own M3U playlists or an
Xtream-codes subscription in `config.toml`:

```toml
[live_tv]
# Include the built-in iptv-org playlist (default: true).
# Set false to use only your own sources.
iptv_org = true

# Extra M3U URLs or local file paths. A leading "~/" is expanded.
playlists = [
  "https://example.com/sports.m3u8",
  "~/playlists/mine.m3u",
]

# Optional Xtream-codes subscription. When server is set, lobster builds the
# get.php m3u_plus URL for you.
[live_tv.xtream]
server = "example.com:8080"
username = "your-username"
password = "your-password"
```

Sources are merged in that order — iptv-org first, then your playlists, then
Xtream — so your own channels appear alongside the free ones.

---

## Use it from a coding agent

`lobster` ships an agent skill so you can ask Claude Code (or another agent)
to find and play something for you.

```bash
mkdir -p ~/.claude/skills
cp -r skills/lobster-play ~/.claude/skills/
```

Then just ask: *"find me The Matrix and play it"*. The agent will show you the
matches and wait for you to pick one before starting playback.

Under the hood it uses three non-interactive commands, which are useful for
scripting on their own:

```bash
lobster find "the matrix" --limit 5      # JSON candidates, each with a ref
lobster find "the bear" --type tv        # filter to movie | tv (case-insensitive)
lobster episodes --ref <REF> --season 2  # JSON season/episode listing
lobster play --ref <REF> --detach        # start playback, return immediately
```

All three print JSON on stdout and never prompt — including on failure. `find`
and `episodes` print nothing else, so their stdout is always parseable; `play`
shares stdout with the player unless you pass `--detach` (see below).

`play --ref` and `episodes --ref` both resolve against the base the ref was
found under, and `play` forwards any flags you pass explicitly (`--base`,
`--quality`, `--player`, `--provider`, `--language`, `--audio-language`,
`--no-subs`, `--debug`, `--continue`) to the detached child so overrides still
apply in the background.

`--detach` is what makes the stdout-is-only-JSON guarantee hold: attached, the
player inherits lobster's stdout and its progress output is interleaved with
the envelope. That is deliberate — a human running `lobster play --ref` should
still see the player — so a script must pass `--detach` and read the player's
output from the `log` path in the response.

Failures use the same envelope — `{"schema": 1, "error": {"code": ..., "message": ...}}`
— so stdout always parses. The exit code is what to branch on:

| Exit | Meaning | What it usually means |
| ---- | ------- | --------------------- |
| `0` | Success | — |
| `1` | Bad invocation | Malformed `ref`, missing `--season`/`--episode`, unknown flag, unrecognised `--type`, `--download` (unsupported by `play`), or an invalid config value. Also internal failures such as an unwritable cache directory. Retrying unchanged will not help |
| `2` | Nothing matched | A misspelling, or a season/episode number the show does not have |
| `3` | Every provider failed | The title exists, the sources are down. Run `lobster doctor`; do not suggest a spelling fix. From `play --detach` it means something narrower — the background process started and then died within a second, and the error message names the log that says why |
| `4` | Player unavailable | mpv (or whichever player is configured) is not installed or not on `PATH` |

Check `schema` before trusting the shape of the rest.

## Configuration

Config file location:
- **Linux/macOS:** `~/.config/lobster/config.toml`
- **Windows:** `%APPDATA%\lobster\config.toml`

```toml
player = "mpv"
quality = "1080"   # or "best" for the highest the source offers (uncapped)
subs_language = "english"
audio_language = "english"   # preferred audio track on multi-dub releases
history = true
auto_next = true
download_dir = "~/Videos/lobster"

# Provider selection (default: moviebox)
# Available: moviebox, flixhq.to, flixhq.ws, soap2day, kimcartoon, yts
# All other providers are automatically used as fallbacks.
# base = "moviebox"

# yts is BitTorrent, not HTTP streaming. It plays while downloading, but it
# also makes you a participant in the swarm: your IP is visible to every peer
# and to the monitoring firms that sit in them, which ordinary streaming never
# does. Use a VPN, or use one of the HTTP providers above. Needs a 64-bit
# build. Pieces land in a temp directory and are removed when playback ends.

# Optional: use a consumet API backend instead of the built-in scraper.
# Self-host from: https://github.com/consumet/api.consumet.org
# When set, lobster uses this API for search, streaming, etc.
# api_url = "https://your-consumet-instance.example.com"

# Live TV sources. See "Live TV and Sports" above.
[live_tv]
iptv_org = true          # include the built-in iptv-org playlist
playlists = []           # extra M3U URLs or local file paths
```

---

## Project Structure

```
lobster/
├── main.go                 # Entry point
├── cmd/                    # CLI commands (Cobra)
│   ├── root.go             # Root command, config loading, global flags
│   ├── search.go           # Search → select → play flow
│   ├── session.go          # Continuous playback loop and episode menu
│   ├── trending.go         # trending and recent commands
│   ├── history.go          # Watch history resume
│   └── version.go          # version command
├── internal/
│   ├── config/             # TOML config loading and validation
│   ├── download/           # ffmpeg-based downloading
│   ├── extract/            # MegaCloud stream URL extraction and decryption
│   ├── history/            # Watch history (TSV storage)
│   ├── httputil/           # Hardened HTTP client, input sanitisation
│   ├── media/              # Shared types (Stream, SearchResult, etc.)
│   ├── player/             # Player backends (mpv, vlc, iina, celluloid)
│   ├── playlist/           # Episode navigation and session state
│   ├── provider/           # Content providers (FlixHQ scraper, Consumet API)
│   ├── subtitle/           # Subtitle download and language matching
│   └── ui/                 # fzf-based terminal UI
├── Makefile
└── go.mod
```

---

## Running Tests

```bash
make test    # Run all tests with race detector
make lint    # Run go vet
```

---

## Security

Built to remove the shell attack surface entirely.

- No shell evaluation — uses `exec.Command` only
- Strict input sanitisation
- Path traversal protection
- TLS 1.2+ enforced
- Randomised mpv IPC sockets
- Safe TOML config parsing (data only)

---

## License

Licensed under the **GNU General Public License v2.0** — see [LICENSE](LICENSE).

Lobster is a Go rewrite of [lobster.sh](https://github.com/justchokingaround/lobster),
which is itself GPL-2.0. GPL-2.0 is copyleft, so this project stays under the same
licence as the work it derives from: you are free to use, study, modify, and
redistribute it, provided derivative works remain GPL-2.0 and ship their source.
