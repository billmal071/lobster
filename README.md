# Lobster — Terminal Media Streamer

![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat&logo=go&logoColor=white)
![Cobra](https://img.shields.io/badge/Cobra-CLI-blue?style=flat)
![fzf](https://img.shields.io/badge/fzf-TUI-hotpink?style=flat)
![mpv](https://img.shields.io/badge/mpv-default%20player-6f4e7c?style=flat)
![VLC](https://img.shields.io/badge/VLC-supported-orange?style=flat)
![IINA](https://img.shields.io/badge/IINA-supported-lightgrey?style=flat)
![ffmpeg](https://img.shields.io/badge/ffmpeg-download-red?style=flat)
![Security](https://img.shields.io/badge/security-hardened-green?style=flat)
![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey?style=flat)

> **Search in your terminal. Stream instantly in your media player.**

Lobster is a security-hardened Go rewrite of [lobster.sh](https://github.com/justchokingaround/lobster). The original was a shell script; this version replaces every unsafe `eval` and shell interpolation with structured, auditable Go code.

---

## Features

- 🔍 Search movies and TV shows by name
- 🔥 Browse trending and recently added content
- 🎬 Stream in mpv, vlc, iina, or celluloid
- ⬇ Download with ffmpeg for offline viewing
- 🌍 Subtitles with automatic language matching
- ▶ Watch history with resume support (`--continue`)
- 🎞 Quality selection — 360p, 480p, 720p, 1080p
- 📦 JSON output mode for scripting and piping

---

## Requirements

1. **Go 1.22+** — build only
2. **fzf** — runtime (interactive menus)
3. **mpv** — default playback
4. **vlc** — alternative playback
5. **iina** — macOS playback
6. **ffmpeg** — required for `--download`

## Installation

```bash
brew install go fzf mpv

git clone https://github.com/billmal071/lobster && cd lobster

go build -o lobster .
```

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
-d, --download <path>
-j, --json
-l, --language <lang>
-n, --no-subs
-p, --provider <name>
-q, --quality <360|480|720|1080>
    --player <mpv|vlc|iina|celluloid>
-x, --debug
```

---

## Project Structure

```
lobster/
├── main.go                 # Entry point
├── cmd/                    # CLI commands (Cobra)
│   ├── root.go             # Root command, config loading, global flags
│   ├── search.go           # Search → select → play flow
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
│   ├── provider/           # FlixHQ scraper and HTML parser
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
