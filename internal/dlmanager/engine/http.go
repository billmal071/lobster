package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// HTTPEngine downloads files via HTTP with byte-range resume.
type HTTPEngine struct {
	Client     *http.Client
	RetryDelay time.Duration // base delay for retries; 0 defaults to 2s
}

func (e *HTTPEngine) Type() string { return "http" }

// Download fetches the full file from streamURL to outputPath.
func (e *HTTPEngine) Download(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error {
	return e.download(ctx, streamURL, outputPath, referer, progressFn, 0)
}

// Resume continues a partial download by checking the .part file size.
func (e *HTTPEngine) Resume(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc) error {
	partPath := outputPath + ".part"
	info, err := os.Stat(partPath)
	if err != nil || info.Size() == 0 {
		return e.Download(ctx, streamURL, outputPath, referer, progressFn)
	}
	return e.download(ctx, streamURL, outputPath, referer, progressFn, info.Size())
}

func (e *HTTPEngine) download(ctx context.Context, streamURL, outputPath, referer string, progressFn ProgressFunc, offset int64) error {
	partPath := outputPath + ".part"

	if err := os.MkdirAll(filepath.Dir(partPath), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			base := e.RetryDelay
			if base == 0 {
				base = 2 * time.Second
			}
			delay := base * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			// Re-check part file size in case previous attempt wrote something.
			if info, err := os.Stat(partPath); err == nil {
				offset = info.Size()
			}
		}

		lastErr = e.doRequest(ctx, streamURL, partPath, referer, progressFn, offset)
		if lastErr == nil {
			return os.Rename(partPath, outputPath)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("after 3 attempts: %w", lastErr)
}

func (e *HTTPEngine) doRequest(ctx context.Context, url, partPath, referer string, progressFn ProgressFunc, offset int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := e.Client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle range response.
	switch resp.StatusCode {
	case http.StatusOK:
		// Server doesn't support range or fresh download. Start from beginning.
		offset = 0
	case http.StatusPartialContent:
		// Range request honored, append to existing file.
	default:
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var totalBytes int64
	if resp.ContentLength > 0 {
		totalBytes = resp.ContentLength + offset
	}

	// Open file for writing.
	flag := os.O_WRONLY | os.O_CREATE
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
		offset = 0
	}

	f, err := os.OpenFile(partPath, flag, 0644)
	if err != nil {
		return fmt.Errorf("opening part file: %w", err)
	}
	defer f.Close()

	// Copy with progress reporting.
	buf := make([]byte, 32*1024)
	written := offset
	lastReport := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return fmt.Errorf("writing: %w", wErr)
			}
			written += int64(n)

			if progressFn != nil && time.Since(lastReport) >= 500*time.Millisecond {
				progressFn(written, totalBytes)
				lastReport = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("reading: %w", readErr)
		}
	}

	// Final progress report.
	if progressFn != nil {
		progressFn(written, totalBytes)
	}

	return nil
}
