// Package download provides secure ffmpeg-based media downloading.
// Uses exec.Command with explicit argument slices and validates
// output paths against directory traversal attacks.
package download

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"lobster/internal/hlsproxy"
	"lobster/internal/httputil"
	"lobster/internal/media"
)

const maxCapturedFFmpegStderrBytes = 64 * 1024

const maxDownloadRetries = 3

// Download fetches a stream to a local file using ffmpeg.
// On failure it retries up to 3 times, resuming from the duration
// already downloaded (detected via ffprobe) using ffmpeg's -ss flag.
func Download(stream *media.Stream, title string, outputDir string, subFile string) (string, error) {
	// Validate ffmpeg is available
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}

	// Create output directory if needed
	absDir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("resolving output directory: %w", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		return "", fmt.Errorf("creating output directory: %w", err)
	}

	// Sanitize filename and validate path
	filename := httputil.SanitizeFilename(title) + ".mkv"
	outputPath, err := httputil.SafeDownloadPath(absDir, filename)
	if err != nil {
		return "", fmt.Errorf("invalid output path: %w", err)
	}

	// Route fake-PNG-obfuscated HLS through the local proxy so ffmpeg receives
	// clean MPEG-TS (it cannot demux the PNG-wrapped segments either).
	if stream.Deobfuscate {
		p, perr := hlsproxy.New(stream.Referer, stream.UserAgent)
		if perr != nil {
			return "", fmt.Errorf("starting de-obfuscation proxy: %w", perr)
		}
		defer p.Close()
		clone := *stream
		clone.URL = p.PlaylistURL(stream.URL)
		stream = &clone
	}

	// Skip if file already exists and appears complete.
	if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
		if isCompleteDownload(outputPath, info.Size()) {
			fmt.Fprintf(os.Stderr, "Already exists, skipping: %s\n", outputPath)
			return outputPath, nil
		}
		fmt.Fprintf(os.Stderr, "Incomplete download detected, will resume: %s\n", outputPath)
	}

	var lastErr error
	for attempt := 0; attempt < maxDownloadRetries; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(os.Stderr, "Retry %d/%d...\n", attempt, maxDownloadRetries-1)
		}

		lastErr = runFFmpegDownload(ffmpegPath, stream, title, subFile, outputPath)
		if lastErr == nil {
			return outputPath, nil
		}

		// Don't delete partial file -- we may resume from it on the next attempt.
		fmt.Fprintf(os.Stderr, "Download attempt failed: %v\n", lastErr)
	}

	// All retries exhausted; clean up partial file.
	os.Remove(outputPath)
	return "", fmt.Errorf("ffmpeg download failed after %d attempts: %w", maxDownloadRetries, lastErr)
}

// probeDuration returns the duration (in seconds) of a media file using ffprobe.
// Returns 0 if the file doesn't exist, ffprobe is unavailable, or the duration
// cannot be determined.
func probeDuration(path string) float64 {
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return 0
	}

	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		return 0
	}

	cmd := exec.Command(ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	durationStr := strings.TrimSpace(string(out))
	if durationStr == "" || durationStr == "N/A" {
		return 0
	}

	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0
	}
	return duration
}

// ffmpegHeaderArgs builds ffmpeg's -headers option for whatever of Referer and
// User-Agent the stream requires, or nothing when it requires neither.
//
// Both go into ONE -headers value. ffmpeg treats -headers as a single option,
// so a second one replaces the first and drops a header rather than adding to
// it. That is an ffmpeg constraint only, and the opposite of what mpv wants:
// -headers is a plain string that takes a CRLF-separated block verbatim,
// whereas mpv's equivalent (--demuxer-lavf-o=headers=) is a list option split
// on commas, which a browser User-Agent's ", like Gecko" makes unparseable. So
// internal/player/headers.go deliberately does NOT pack them together — it
// emits --referrer and --user-agent separately.
//
// The User-Agent is not optional where a source asks for one. ffmpeg defaults
// to "Lavf/<version>", and at least one CDN lobster reaches
// (cdn.animeonsen.xyz) denies that exact value with 403 while serving any
// other — so without this, a stream whose provider set UserAgent fails on its
// very first request, with an access error that reads like a dead host.
func ffmpegHeaderArgs(stream *media.Stream) []string {
	var b strings.Builder
	if stream.Referer != "" {
		b.WriteString("Referer: " + stream.Referer + "\r\n")
	}
	if stream.UserAgent != "" {
		b.WriteString("User-Agent: " + stream.UserAgent + "\r\n")
	}
	if b.Len() == 0 {
		return nil
	}
	return []string{"-headers", b.String()}
}

// ffmpegDownloadArgs builds the full ffmpeg command line for one download
// attempt.
//
// Separated from runFFmpegDownload so it can be asserted on without executing
// ffmpeg. That matters for more than tidiness: the headers below are the only
// thing standing between a CDN that requires them and a 403 on the first
// request, and a test that stopped at ffmpegHeaderArgs would prove the value
// is built correctly while saying nothing about whether it is passed. CI has
// no ffmpeg, so running it was never an option either.
func ffmpegDownloadArgs(stream *media.Stream, title, subFile, targetPath string, resumeFromSec float64) []string {
	args := []string{
		"-y", // Overwrite output (for partial/empty files)
	}

	// Pass whatever headers the CDN requires. See ffmpegHeaderArgs: both go
	// into one -headers value, and the User-Agent is load-bearing.
	args = append(args, ffmpegHeaderArgs(stream)...)

	// If resuming, seek past already-downloaded content on the input side.
	if resumeFromSec > 0 {
		// Back up 2 seconds to avoid missing frames at the boundary.
		seekSec := resumeFromSec - 2
		if seekSec < 0 {
			seekSec = 0
		}
		args = append(args, "-ss", strconv.FormatFloat(seekSec, 'f', 3, 64))
	}

	args = append(args, "-i", stream.URL)

	// Add subtitle if available
	if subFile != "" {
		args = append(args,
			"-i", subFile,
			"-c:s", "srt", // Convert subtitles to SRT for MKV
		)
	}

	args = append(args,
		"-c:v", "copy", // Copy video stream (no re-encoding)
		"-c:a", "copy", // Copy audio stream
	)

	if subFile != "" {
		args = append(args,
			"-map", "0:v", // Video from first input
			"-map", "0:a", // Audio from first input
			"-map", "1:s", // Subtitles from second input
		)
	}

	// Add metadata
	return append(args,
		"-metadata", fmt.Sprintf("title=%s", title),
		"-progress", "pipe:1",
		"-nostats",
		targetPath,
	)
}

// runFFmpegDownload runs a single ffmpeg download attempt. If a partial file
// already exists at outputPath, it probes its duration and uses -ss to seek
// past the already-downloaded content, appending to a temporary file and then
// concatenating the parts.
func runFFmpegDownload(ffmpegPath string, stream *media.Stream, title, subFile, outputPath string) error {
	// Check if we can resume from a partial download.
	resumeFromSec := probeDuration(outputPath)

	targetPath := outputPath
	if resumeFromSec > 0 {
		// When resuming, write to a temporary file; we'll concatenate after.
		targetPath = outputPath + ".part"
	}
	args := ffmpegDownloadArgs(stream, title, subFile, targetPath, resumeFromSec)

	cmd := exec.Command(ffmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating ffmpeg stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("creating ffmpeg stderr pipe: %w", err)
	}

	if resumeFromSec > 0 {
		fmt.Fprintf(os.Stderr, "Resuming download from %s: %s\n",
			formatProgressTime(int64(resumeFromSec*1_000_000)), outputPath)
	} else {
		fmt.Fprintf(os.Stderr, "Downloading to: %s\n", outputPath)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}

	var stderrBuf bytes.Buffer
	stderrWriter := &tailLimitedBuffer{
		buf:   &stderrBuf,
		limit: maxCapturedFFmpegStderrBytes,
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(stderrWriter, stderr)
	}()

	progress := &ffmpegProgress{}
	printedProgress := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		updated, _ := progress.updateFromLine(line)
		if !updated {
			continue
		}
		printedProgress = true
		fmt.Fprintf(os.Stderr, "\r%s", progress.renderLine())
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		<-stderrDone
		return fmt.Errorf("reading ffmpeg progress: %w", err)
	}

	runErr := cmd.Wait()
	<-stderrDone
	if printedProgress {
		fmt.Fprintln(os.Stderr)
	}

	if runErr != nil {
		// Don't delete partial file -- leave it for resume on retry.
		os.Remove(targetPath) // Remove the .part file on failure though.
		errOutput := strings.TrimSpace(stderrBuf.String())
		if errOutput == "" {
			return fmt.Errorf("ffmpeg failed: %w", runErr)
		}
		return fmt.Errorf("ffmpeg failed: %w\n%s", runErr, errOutput)
	}

	// If we were resuming, concatenate the original partial file with the new part.
	if resumeFromSec > 0 {
		if err := concatMKVFiles(ffmpegPath, outputPath, targetPath); err != nil {
			// Concat failed; the .part file has the new segment at least.
			// Replace the original with just the new part so we don't lose progress.
			os.Rename(targetPath, outputPath)
			return fmt.Errorf("concatenating resume parts: %w", err)
		}
		os.Remove(targetPath)
	}

	return nil
}

// concatMKVFiles concatenates two MKV files using ffmpeg's concat demuxer.
// The result replaces the first file.
func concatMKVFiles(ffmpegPath, file1, file2 string) error {
	concatList := file1 + ".concat.txt"
	content := fmt.Sprintf("file '%s'\nfile '%s'\n", file1, file2)
	if err := os.WriteFile(concatList, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing concat list: %w", err)
	}
	defer os.Remove(concatList)

	merged := file1 + ".merged.mkv"
	cmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatList,
		"-c", "copy",
		merged,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(merged)
		return fmt.Errorf("ffmpeg concat: %w\n%s", err, string(out))
	}

	// Replace original with merged file.
	if err := os.Rename(merged, file1); err != nil {
		os.Remove(merged)
		return err
	}

	return nil
}

// isCompleteDownload checks if a downloaded file appears to be complete
// by verifying it meets a minimum size and ffprobe can read a valid duration.
func isCompleteDownload(path string, size int64) bool {
	// Files under 1 MiB are almost certainly incomplete for video
	const minSize = 1024 * 1024
	if size < minSize {
		return false
	}

	// Try ffprobe to check if the file has a valid duration
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		// ffprobe not available — fall back to size-only check
		return size >= minSize
	}

	cmd := exec.Command(ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	durationStr := strings.TrimSpace(string(out))
	if durationStr == "" || durationStr == "N/A" {
		return false
	}

	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return false
	}

	// A valid video should have at least 10 seconds of content
	return duration >= 10.0
}

type tailLimitedBuffer struct {
	buf   *bytes.Buffer
	limit int
}

func (t *tailLimitedBuffer) Write(p []byte) (int, error) {
	if t.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= t.limit {
		t.buf.Reset()
		_, _ = t.buf.Write(p[len(p)-t.limit:])
		return len(p), nil
	}
	if t.buf.Len()+len(p) > t.limit {
		drop := t.buf.Len() + len(p) - t.limit
		if drop > 0 {
			existing := t.buf.Bytes()
			t.buf.Reset()
			if drop < len(existing) {
				_, _ = t.buf.Write(existing[drop:])
			}
		}
	}
	_, _ = t.buf.Write(p)
	return len(p), nil
}

type ffmpegProgress struct {
	outTimeMS int64
	totalSize int64
	speed     string
}

func (p *ffmpegProgress) updateFromLine(line string) (bool, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return false, false
	}

	switch key {
	case "out_time_ms":
		ms, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false, false
		}
		if ms != p.outTimeMS {
			p.outTimeMS = ms
			return true, false
		}
	case "total_size":
		size, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return false, false
		}
		if size != p.totalSize {
			p.totalSize = size
			return true, false
		}
	case "speed":
		if value != "" && value != p.speed {
			p.speed = value
			return true, false
		}
	case "progress":
		return false, value == "end"
	}

	return false, false
}

func (p *ffmpegProgress) renderLine() string {
	parts := []string{"Downloading"}
	if p.outTimeMS > 0 {
		parts = append(parts, fmt.Sprintf("time %s", formatProgressTime(p.outTimeMS)))
	}
	if p.totalSize > 0 {
		parts = append(parts, fmt.Sprintf("size %s", formatProgressBytes(p.totalSize)))
	}
	if p.speed != "" {
		parts = append(parts, fmt.Sprintf("speed %s", p.speed))
	}
	return strings.Join(parts, " | ")
}

func formatProgressTime(microseconds int64) string {
	totalSeconds := microseconds / 1_000_000
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func formatProgressBytes(size int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)

	switch {
	case size >= gib:
		return fmt.Sprintf("%.1f GiB", float64(size)/float64(gib))
	case size >= mib:
		return fmt.Sprintf("%.1f MiB", float64(size)/float64(mib))
	case size >= kib:
		return fmt.Sprintf("%.0f KiB", float64(size)/float64(kib))
	default:
		return fmt.Sprintf("%d B", size)
	}
}
