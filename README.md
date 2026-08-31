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
- 📦 JSON output mode for scripting and piping

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
# Available: moviebox, flixhq.to, flixhq.ws, soap2day, kimcartoon
# All other providers are automatically used as fallbacks.
# base = "moviebox"

# Optional: use a consumet API backend instead of the built-in scraper.
# Self-host from: https://github.com/consumet/api.consumet.org
# When set, lobster uses this API for search, streaming, etc.
# api_url = "https://your-consumet-instance.example.com"
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
