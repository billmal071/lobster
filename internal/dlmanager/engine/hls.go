package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lobster/internal/dlmanager/store"
)

// HLSEngine downloads HLS streams by fetching segments and muxing with ffmpeg.
type HLSEngine struct {
	Client      *http.Client
	Store       *store.Store
	RetryDelay  time.Duration // base delay for retries; 0 defaults to 2s
	SubtitleURL string        // if set, embed subtitle during mux (requires ffmpeg)
}

func (e *HLSEngine) Type() string { return "hls" }

// Download fetches an HLS stream: parse m3u8, download segments, mux to output.
func (e *HLSEngine) Download(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error {
	return e.downloadHLS(ctx, streamURL, outputPath, referer, progressFn, 0)
}

// Resume continues an HLS download from where it left off using segment tracking.
func (e *HLSEngine) Resume(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error {
	// Extract download ID from store by matching output path.
	return e.downloadHLS(ctx, streamURL, outputPath, referer, progressFn, 0)
}

// ResumeWithID continues an HLS download using a known download ID for segment tracking.
func (e *HLSEngine) ResumeWithID(ctx context.Context, downloadID int, streamURL, outputPath, referer string, progressFn ProgressFunc) error {
	return e.downloadHLS(ctx, streamURL, outputPath, referer, progressFn, downloadID)
}

func (e *HLSEngine) downloadHLS(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc, downloadID int) error {
	partsDir := outputPath + ".parts"
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		return fmt.Errorf("creating parts dir: %w", err)
	}

	// Fetch and parse the m3u8 playlist.
	segmentURLs, err := e.fetchPlaylist(ctx, streamURL, referer)
	if err != nil {
		return fmt.Errorf("fetching playlist: %w", err)
	}
	if len(segmentURLs) == 0 {
		return fmt.Errorf("no segments found in playlist")
	}

	totalSegments := len(segmentURLs)

	// If we have a download ID, use segment tracking from the store.
	var completedSet map[int]bool
	if downloadID > 0 {
		existingSegs, err := e.Store.GetSegments(downloadID)
		if err == nil && len(existingSegs) > 0 {
			completedSet = make(map[int]bool)
			for _, seg := range existingSegs {
				if seg.Completed {
					completedSet[seg.Idx] = true
				}
			}
		} else {
			// First time: insert segments into store.
			segs := make([]store.Segment, totalSegments)
			for i, u := range segmentURLs {
				segs[i] = store.Segment{Idx: i, URL: u}
			}
			if err := e.Store.InsertSegments(downloadID, segs); err != nil {
				return fmt.Errorf("inserting segments: %w", err)
			}
		}
	}

	// Download each segment.
	doneCount := 0
	if completedSet != nil {
		doneCount = len(completedSet)
	}

	for i, segURL := range segmentURLs {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip already completed segments.
		if completedSet != nil && completedSet[i] {
			continue
		}

		segPath := filepath.Join(partsDir, fmt.Sprintf("seg_%05d.ts", i))

		if err := e.downloadSegment(ctx, segURL, segPath, referer); err != nil {
			return fmt.Errorf("downloading segment %d: %w", i, err)
		}

		doneCount++

		// Mark segment done in store.
		if downloadID > 0 {
			e.Store.MarkSegmentDone(downloadID, i)
		}

		if progressFn != nil {
			progressFn(int64(doneCount), int64(totalSegments))
		}
	}

	// Mux segments into final file using ffmpeg.
	if err := e.muxSegments(ctx, partsDir, outputPath, totalSegments); err != nil {
		return fmt.Errorf("muxing segments: %w", err)
	}

	// Clean up parts directory.
	os.RemoveAll(partsDir)

	return nil
}

func (e *HLSEngine) downloadSegment(ctx context.Context, segURL, segPath, referer string) error {
	base := e.RetryDelay
	if base == 0 {
		base = 2 * time.Second
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := base * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		lastErr = e.doSegmentDownload(ctx, segURL, segPath, referer)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("after 3 attempts: %w", lastErr)
}

func (e *HLSEngine) doSegmentDownload(ctx context.Context, segURL, segPath, referer string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, segURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("segment status %d", resp.StatusCode)
	}

	f, err := os.Create(segPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

func (e *HLSEngine) fetchPlaylist(ctx context.Context, playlistURL, referer string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, playlistURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, err
	}

	segments, isMaster := parseM3U8(string(body), playlistURL)

	if isMaster && len(segments) > 0 {
		// segments contains variant playlist URLs; fetch the best one (last = highest bandwidth).
		return e.fetchPlaylist(ctx, segments[len(segments)-1], referer)
	}

	return segments, nil
}

// parseM3U8 parses an HLS playlist. Returns segment/variant URLs and whether it's a master playlist.
func parseM3U8(body string, baseURL string) (urls []string, isMaster bool) {
	base, _ := url.Parse(baseURL)

	scanner := bufio.NewScanner(strings.NewReader(body))
	isMaster = false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			isMaster = true
			continue
		}

		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// This is a URL line (segment or variant playlist).
		resolved := resolveURL(base, line)
		urls = append(urls, resolved)
	}

	return urls, isMaster
}

// resolveURL resolves a potentially relative URL against a base.
func resolveURL(base *url.URL, ref string) string {
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return base.ResolveReference(refURL).String()
}

// muxSegments concatenates .ts segments into a single output file.
// Uses ffmpeg when subtitles need embedding; otherwise concatenates directly.
func (e *HLSEngine) muxSegments(ctx context.Context, partsDir, outputPath string, totalSegments int) error {
	// Use ffmpeg only when we need subtitle embedding.
	if e.SubtitleURL != "" {
		return e.muxWithFFmpeg(ctx, partsDir, outputPath, totalSegments)
	}
	return e.concatDirect(partsDir, outputPath, totalSegments)
}

// muxWithFFmpeg uses ffmpeg to concatenate segments and optionally embed subtitles.
func (e *HLSEngine) muxWithFFmpeg(ctx context.Context, partsDir, outputPath string, totalSegments int) error {
	absOutput, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("resolving output path: %w", err)
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		// Fallback to direct concat (subtitles won't be embedded).
		return e.concatDirect(partsDir, outputPath, totalSegments)
	}

	concatPath := filepath.Join(partsDir, "concat.txt")
	var lines []string
	for i := 0; i < totalSegments; i++ {
		lines = append(lines, "file '"+fmt.Sprintf("seg_%05d.ts", i)+"'")
	}
	if err := os.WriteFile(concatPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("writing concat file: %w", err)
	}

	args := []string{"-y", "-f", "concat", "-safe", "0", "-i", concatPath}

	if e.SubtitleURL != "" {
		args = append(args, "-i", e.SubtitleURL, "-c:v", "copy", "-c:a", "copy", "-c:s", "srt",
			"-map", "0:v", "-map", "0:a", "-map", "1:s")
	} else {
		args = append(args, "-c", "copy")
	}

	args = append(args, absOutput)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	cmd.Dir = partsDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg mux: %w\n%s", err, string(output))
	}

	return nil
}

// concatDirect concatenates .ts files directly when ffmpeg is unavailable.
func (e *HLSEngine) concatDirect(partsDir, outputPath string, totalSegments int) error {
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i := 0; i < totalSegments; i++ {
		segPath := filepath.Join(partsDir, fmt.Sprintf("seg_%05d.ts", i))
		f, err := os.Open(segPath)
		if err != nil {
			return fmt.Errorf("opening segment %d: %w", i, err)
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return fmt.Errorf("copying segment %d: %w", i, err)
		}
		f.Close()
	}

	return nil
}

// parseBandwidth extracts BANDWIDTH from an EXT-X-STREAM-INF line.
func parseBandwidth(line string) int {
	for _, part := range strings.Split(line, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "BANDWIDTH=") {
			val := strings.TrimPrefix(part, "BANDWIDTH=")
			n, _ := strconv.Atoi(val)
			return n
		}
	}
	return 0
}
