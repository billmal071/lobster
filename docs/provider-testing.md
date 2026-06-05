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
   - `flixhq.ws` (default) — browsing, trending, metadata
   - `soap2day` — streaming via TMDB + moviesapi
   - `tbcpl` / `1shows.org` — streaming via Vidzee
   - `moviebox` — MovieBox V3 mobile API
   - `flixhq.to` — legacy FlixHQ scraper
   - `kimcartoon.com.co` — cartoons/anime

   All other providers are automatically used as fallbacks when the primary fails.

## 2. Test Provider via CLI

Use the `--base` flag to force a specific provider:

### Example commands

```bash
# Test with FlixHQ.ws (default)
lobster --base flixhq.ws trending

# Test with Soap2Day
lobster --base soap2day "Inception"

# Test with TBCPL
lobster --base tbcpl "Inception"

# Test with KimCartoon
lobster --base kimcartoon.com.co "SpongeBob"
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
A: Use the TUI (run `lobster` with no args) — categories like Movies, Series, Cartoons use different providers. Or switch with `--base` on the CLI.

**Q: What if a provider is down?**  
A: Lobster automatically falls back to the next provider in the chain: Soap2Day → MovieBox → TBCPL → FlixHQWS → FlixHQ → KimCartoon.

---

*Keep this guide updated as new providers are added or existing ones change their APIs.*
