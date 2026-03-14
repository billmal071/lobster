# Batch Season Download

## Overview

Add the ability to download all episodes in a season (or a range of episodes) in one command, with organized folder output, skip-on-fail behavior, and retry prompt for failed downloads.

## Current Behavior

Downloads are single-episode only. The user selects one episode, it resolves the stream, and downloads via ffmpeg to a flat directory.

## Design

### Episode Picker Changes

When the `-d` (download) flag is set and content type is TV, inject two special options at the top of the episode selection list:

- **Download all episodes** — downloads every episode in the selected season
- **Download range (e.g., 1-5)** — prompts for a range, then downloads those episodes

Selecting a single episode works as before (no behavior change).

### Range Format

Supported range formats:
- `1-5` — inclusive range (episodes 1 through 5)
- `3,7,9` — comma-separated individual episodes
- `1-3,7,10-12` — mixed ranges and individual episodes

Range input is collected via `ui.Input`. Invalid ranges produce an error message and re-prompt.

### Download Flow (Batch)

For each episode in the batch:

1. Resolve the stream: get servers, get embed URL, extract stream (same as single-episode flow)
2. Download via ffmpeg with subtitles (if enabled)
3. On failure: log the error to stderr, record the episode as failed, continue to the next episode
4. Print progress per episode: `[3/18] Downloading E03 - The Twins...`

After all episodes are attempted, print a summary:
```
Downloaded 16/18 episodes. Failed: E05, E12
Retry failed downloads? > Yes / No
```

If the user selects Yes, re-attempt only the failed episodes with the same skip-on-fail behavior. This loops until all succeed or the user declines.

### Folder Structure

Batch downloads use an organized nested structure:

```
<download_dir>/<Show Title>/Season <NN>/E<NN> - <Episode Title>.mkv
```

Example:
```
~/Videos/lobster/Star Wars Visions/Season 01/E01 - The Duel.mkv
~/Videos/lobster/Star Wars Visions/Season 01/E02 - Tatooine Rhapsody.mkv
```

- Show title and episode titles are sanitized with the existing `SanitizeFilename` function
- Season number is zero-padded to 2 digits
- Episode number is zero-padded to 2 digits
- Single-episode downloads (non-batch) continue to use the existing flat structure to avoid breaking current behavior

### Sequential Execution

Downloads run one at a time. This avoids rate-limiting from the streaming provider and makes progress output clear.

## Files to Modify

- `cmd/search.go` — update `resolveAndPlay` to handle batch download mode; add batch episode selection logic and retry loop
- `internal/download/download.go` — no changes needed (called per-episode as-is)
- `internal/ui/ui.go` — no changes needed (`Select` and `Input` already support what's needed)
- `internal/httputil/sanitize.go` — no changes needed (`SanitizeFilename` and `SafeDownloadPath` already exist)

New code will be added to `cmd/search.go`:
- `parseEpisodeRange(input string, maxEpisode int) ([]int, error)` — parses range strings
- `batchDownload(...)` — orchestrates the per-episode resolve+download loop with skip-on-fail and retry

## Edge Cases

- **Empty range / no valid episodes**: error message, re-prompt
- **Range exceeds available episodes**: clamp to available range, warn user
- **All episodes fail**: still prompt retry (user might want to try again after fixing network)
- **User cancels mid-batch** (Ctrl+C): ffmpeg cleans up partial file (already handled), batch stops
- **Episode title missing**: fall back to `E<NN>` without title suffix
