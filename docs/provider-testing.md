# Provider Testing Guide for Lobster

Lobster supports multiple content providers via the `--provider` flag or the `provider` setting in `config.toml`. This document outlines how to test each provider to ensure they are working correctly.

## 1. Verify Provider Configuration

1. **Check the configuration file**  
   - **Linux/macOS**: `~/.config/lobster/config.toml`  
   - **Windows**: `%APPDATA%\\lobster\\config.toml`  

   Ensure the `provider` key is set to the desired provider name, e.g.:

   ```toml
   provider = "moviebox"
   ```

2. **List available providers** (for reference)  
   The default providers are:
   - `moviebox`
   - `flixhq.to`
   - `flixhq.ws`
   - `soap2day`
   - `kimcartoon`
   - (others are fallback providers)

## 2. Test Provider via CLI

Run the binary with the `--provider` flag to force a specific provider, regardless of config.

### Example commands

```bash
# Test moviebox (default)
./lobster --provider moviebox trending

# Test FlixHQ (web)
./lobster --provider flixhq.to trending

# Test FlixHQ WS (WebSocket)
./lobster --provider flixhq.ws trending

# Test KimCartoon
./lobster --provider kimcartoon trending

# Test Soap2Day
./lobster --provider soap2day trending
```

### Expected output

- **Successful provider**: The command should return a list of trending titles, then allow you to select and play a title using mpv, VLC, etc.
- **Error handling**: If the provider fails, you should see an error message indicating the provider name and the failure reason (e.g., network error, parsing error).

## 3. Validate Provider Functionality

For each provider, verify the following aspects:

| Aspect | How to Test | Success Criteria |
|--------|-------------|------------------|
| **Search** | `./lobster --provider <name> search "Inception"` | Returns search results matching the query. |
| **Streaming** | `./lobster --provider <name> play "Inception"` (or use the UI to select) | Video streams without errors; playback controls work. |
| **Subtitle matching** | `./lobster --provider <name> search "Inception" --language english` | Correct subtitle file is downloaded and synced. |
| **Download** | `./lobster --provider <name> download "Inception" --quality 720p` | Download completes, file saved to `download_dir`. |
| **Episode navigation** | Use UI to go to next/previous episode after selecting a series | Episode list updates correctly; navigation works. |
| **Resume support** | Start playback, stop, then run with `--continue` | Playback resumes from the last position. |

## 4. Check Logs & Errors

- **Verbose mode**: Add `-v` or `--debug` flag to see detailed logs.
- **Log files**: Errors may also be written to `~/.config/lobster/logs/` (Linux/macOS) or `%LOCALAPPDATA%\\lobster\\logs\` (Windows).

## 5. Common Issues & Fixes

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| "Provider not found" | Provider name typo or not compiled in | Verify provider name matches one of the listed names; rebuild if you added a new provider. |
| No results returned | Provider API changed or rate‑limited | Check provider status page; add a delay or retry logic (already handled in code). |
| Download fails | `ffmpeg` not in PATH or permission issue | Ensure `ffmpeg` is installed and accessible; verify write permission to `download_dir`. |
| Subtitles missing | Language not supported by provider | Verify provider supports the requested language; check provider docs. |
| Player not launching | Player binary missing or not in PATH | Install required player (mpv, vlc, iina, celluloid) and ensure it's in your system PATH. |

## 6. Automated Provider Tests (Optional)

If you want to add CI checks:

1. **Create integration tests** in `internal/provider/` using the existing `*_test.go` files as templates.
2. **Run `make test`** to execute all tests, including provider-specific tests.
3. **Add a GitHub Actions workflow** that runs `make test` on each push.

## 7. FAQ

**Q: How do I know which provider is being used?**  
A: The CLI prints the selected provider at startup (debug mode) and logs the provider name when a request is made.

**Q: Can I test multiple providers in one session?**  
A: Yes, you can switch providers on the fly using the `--provider` flag or by editing `config.toml` and restarting the CLI.

**Q: What if a provider is down?**  
A: Lobster automatically falls back to the next provider in the list (as defined in the code). You can also force a fallback by setting `fallback = true` in `config.toml`.

---

*Keep this guide updated as new providers are added or existing ones change their APIs.*