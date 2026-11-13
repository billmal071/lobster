# Provider Testing Guide for Lobster

Lobster selects the content provider via the `base` setting in `config.toml` or the `--base` CLI flag. This document outlines how to test each provider.

## 1. Verify Provider Configuration

1. **Check the configuration file**  
   - **Linux/macOS**: `~/.config/lobster/config.toml`  
   - **Windows**: `%APPDATA%\lobster\config.toml`  

   Set the `base` key to the desired provider, e.g.:

   ```toml
   base = "flixhq.ws"
   ```

2. **Available providers**  
   - `auto` (default) — FlixHQWS for search and series; films routed to YTS
   - `flixhq.ws` — scraper; browsing, trending, metadata
   - `tbcpl` / `1shows.org` — streaming via Vidzee
   - `animeonsen` — anime
   - `yts` — films over BitTorrent
   - `eztv` — TV series over BitTorrent, keyed by IMDb id
   - `nyaa` — anime over BitTorrent (English-translated category; the feed
     exposes only the 75 newest torrents per query)

   Retired (`--base` rejects them; a config file's value becomes `auto` with a
   warning): `soap2day`, `moviebox`, `vidnest`, `flixhq.to`, `kimcartoon`,
   `allanime`, `anipub`.

   All other providers are automatically used as fallbacks when the primary
   fails; the torrent sources (`yts`, `eztv`, `nyaa`) only when
   `torrent_fallback = true`, because a magnet exposes your IP to the swarm.

## 2. Test Provider via CLI

Use the `--base` flag to force a specific provider:

### Example commands

```bash
# Test with FlixHQ.ws
lobster --base flixhq.ws trending

# Test with TBCPL
lobster --base tbcpl "Inception"

# Test with AnimeOnsen
lobster --base animeonsen "One Punch Man"
```

### Expected output

- **Successful provider**: Returns results, then allows you to select and play.
- **Error handling**: If the provider fails, fallback providers are tried automatically.

## 3. Validate Provider Functionality

For each provider, verify:

| Aspect | How to Test | Success Criteria |
|--------|-------------|------------------|
| **Search** | `lobster --base <name> "Inception"` | Returns search results matching the query. |
| **Streaming** | Select a result and press Enter | Video streams without errors. |
| **Subtitles** | `lobster -l english "Inception"` | Subtitle file downloaded and loaded in player. |
| **Download** | `lobster -d ~/Videos "Inception"` | Download completes, file saved. |
| **Episode navigation** | Select a series, play an episode, then use next/prev | Episode list works; navigation works. |
| **Resume support** | Start playback, stop, then `lobster -c` | Playback resumes from last position. |

## 4. Check Logs & Errors

- **Debug mode**: Add `-x` flag to see detailed logs.

```bash
lobster -x "Inception"
```

Debug output shows which provider is selected, which fallbacks are tried, and subtitle resolution.

## 5. Common Issues & Fixes

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| No results returned | Provider API changed or rate-limited | Try a different `--base`; fallbacks will be tried automatically. |
| Download fails | `ffmpeg` not in PATH | Install ffmpeg and ensure it's in your PATH. |
| Subtitles missing | SubDL API key not configured | Default key is bundled; check debug output with `-x`. |
| Player not launching | Player binary missing | Install mpv, vlc, iina, or celluloid. |
| Stream URL expired | Subtitle downloads took too long | Subtitles are capped at 3 to reduce delay. |

## 6. Automated Provider Tests

```bash
# Run all tests including provider tests
go test ./... -count=1

# Run only provider tests
go test ./internal/provider/ -v -count=1
```

Provider tests use httptest servers with mocked responses and don't hit real APIs.

## 7. FAQ

**Q: How do I know which provider is being used?**  
A: Run with `-x` (debug mode). It prints the selected provider and all fallback attempts.

**Q: Can I test multiple providers in one session?**  
A: Switch with `--base` on the CLI; each command is an independent check of that source. The TUI (run `lobster` with no args) is not an independent check per category: under the default config Movies and Series both search FlixHQWS and only Anime searches AnimeOnsen, and Movies and Series searches can add rows from fallback providers when the primary returns few results (the Anime tab never does).

**Q: What if a provider is down?**  
A: Lobster builds a fallback chain from every source except the primary: TBCPL, Consumet (when `api_url` is set), FlixHQWS, AnimeOnsen, TBCPLEmbed (when the catalog feed is on), and, only when `torrent_fallback` is on, EZTV, Nyaa and YTS. The probes are health-ranked and raced, not tried strictly in sequence, and the torrent sources are tried only after every HTTP source has failed. When `api_url` is set, Consumet is the primary rather than a fallback, and `base` is ignored. Under the default config FlixHQWS is the primary for series and for any film YTS has no match for, so its fallbacks start at TBCPL; with `base = "auto"` a film is routed to YTS at play time instead (not with `--json`, or `--download`, which keep FlixHQWS).

---

*Keep this guide updated as new providers are added or existing ones change their APIs.*
