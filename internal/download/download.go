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

	"lobster/internal/httputil"
	"lobster/internal/media"
)

const maxCapturedFFmpegStderrBytes = 64 * 1024

// Download fetches a stream to a local file using ffmpeg.
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

	// Skip if file already exists and has content
	if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
		fmt.Fprintf(os.Stderr, "Already exists, skipping: %s\n", outputPath)
		return outputPath, nil
	}

	// Build ffmpeg args as explicit slice
	args := []string{
		"-y", // Overwrite output (for partial/empty files)
	}

	// Pass Referer and headers for CDNs that require them
	if stream.Referer != "" {
		args = append(args, "-headers", "Referer: "+stream.Referer+"\r\n")
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
	args = append(args,
		"-metadata", fmt.Sprintf("title=%s", title),
		"-progress", "pipe:1",
		"-nostats",
		outputPath,
	)

	cmd := exec.Command(ffmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating ffmpeg stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("creating ffmpeg stderr pipe: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Downloading to: %s\n", outputPath)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting ffmpeg: %w", err)
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
		return "", fmt.Errorf("reading ffmpeg progress: %w", err)
	}

	runErr := cmd.Wait()
	<-stderrDone
	if printedProgress {
		fmt.Fprintln(os.Stderr)
	}

	if runErr != nil {
		// Clean up partial download on failure
		os.Remove(outputPath)
		errOutput := strings.TrimSpace(stderrBuf.String())
		if errOutput == "" {
			return "", fmt.Errorf("ffmpeg download failed: %w", runErr)
		}
		return "", fmt.Errorf("ffmpeg download failed: %w\n%s", runErr, errOutput)
	}

	return outputPath, nil
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
