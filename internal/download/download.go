// Package download provides secure ffmpeg-based media downloading.
// Uses exec.Command with explicit argument slices and validates
// output paths against directory traversal attacks.
package download

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

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

	// Build ffmpeg args as explicit slice
	args := []string{
		"-y", // Overwrite output
		"-i", stream.URL,
	}

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
		outputPath,
	)

	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Fprintf(os.Stderr, "Downloading to: %s\n", outputPath)

	if err := cmd.Run(); err != nil {
		// Clean up partial download on failure
		os.Remove(outputPath)
		return "", fmt.Errorf("ffmpeg download failed: %w", err)
	}

	return outputPath, nil
}
