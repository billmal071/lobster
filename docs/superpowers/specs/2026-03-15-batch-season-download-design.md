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

These two items are prepended to the fzf list. The index returned by `ui.Select` must be offset by 2 to map back to the actual episodes array (0 = all, 1 = range, 2+ = individual episode at index N-2).

Selecting a single episode works as before (no behavior change).

### Range Format

Ranges refer to **episode numbers as displayed to the user** (from `media.Episode.Number`), not list positions. Each requested number is matched against the actual episode list. Episodes whose number doesn't exist in the season produce a warning on stderr (e.g., `Warning: episode 6 not found, skipping`) rather than a silent skip.

Supported range formats:
- `1-5` — inclusive range (episodes 1 through 5)
- `3,7,9` — comma-separated individual episodes
- `1-3,7,10-12` — mixed ranges and individual episodes

Range input is collected via `ui.Input`. Invalid ranges produce an error message and re-prompt.

### Download Flow (Batch)

For each episode in the batch:

1. Print progress: `[3/18] Downloading E03 - The Twins...` (before ffmpeg starts)
2. Resolve the stream: get servers, get embed URL, extract stream (same as single-episode flow)
3. Handle subtitles: create a temp dir, download subtitle, pass to ffmpeg. Explicitly clean up the temp dir after each episode completes (do NOT use `defer` in the loop — clean up at end of each iteration to avoid stacking)
4. Download via ffmpeg. In batch mode, suppress ffmpeg's verbose stderr output (redirect to /dev/null) so it doesn't interleave with progress lines
5. On failure: log the error to stderr, record the episode as failed, continue to the next episode

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

The batch caller constructs the full nested directory path and passes it as `outputDir` to `download.Download`. The `title` parameter passed to `Download` is just the episode filename stem (e.g., `E01 - The Duel`), not the full `ShowName S01E01` format — `Download` appends `.mkv` and sanitizes it.

- Show title and episode titles are sanitized with the existing `SanitizeFilename` function
- Season number is zero-padded to 2 digits
- Episode number is zero-padded to 2 digits
- Single-episode downloads (non-batch) continue to use the existing flat structure to avoid breaking current behavior

### Sequential Execution

Downloads run one at a time. This avoids rate-limiting from the streaming provider and makes progress output clear.

### Flag Interactions

- **`--json` (`-j`) + batch**: unsupported. If both `-d` and `-j` are set in batch mode, error out with a message: `--json is not supported with batch downloads`
- **`--continue` (`-c`)**: ignored in download mode (it's for playback resume). No special handling needed.

## Files to Modify

- `cmd/search.go` — update `resolveAndPlay` to detect batch mode and delegate to batch logic
- **New file: `cmd/batch.go`** — contains all batch download logic, keeping `search.go` focused on search-select-play:
  - `parseEpisodeRange(input string, episodes []media.Episode) ([]media.Episode, error)` — parses range strings and matches against actual episode list
  - `batchDownload(p provider.Provider, selected media.SearchResult, episodes []media.Episode, season media.Season) error` — orchestrates the per-episode resolve+download loop with skip-on-fail and retry
- `internal/download/download.go` — no changes needed (called per-episode as-is)
- `internal/ui/ui.go` — no changes needed
- `internal/httputil/sanitize.go` — no changes needed

## Edge Cases

- **Empty range / no valid episodes**: error message, re-prompt
- **Range exceeds available episodes**: download episodes that exist, warn about missing numbers on stderr
- **Non-contiguous episode numbers**: ranges match by episode number, not position; gaps are handled gracefully
- **All episodes fail**: still prompt retry (user might want to try again after fixing network)
- **User cancels mid-batch** (Ctrl+C): Go process terminates; partial ffmpeg file may remain (known limitation — no signal handler added to keep scope small)
- **Episode title missing**: fall back to `E<NN>` without title suffix
- **Dead code cleanup**: fix the unreachable `if dir == ""` check inside the existing `if flagDownload != ""` block in `search.go`
