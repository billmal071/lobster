// Package poster downloads and renders poster images for terminal display.
// Uses chafa for high-quality rendering when available, falls back to
// half-block ANSI art with bicubic downscaling.
package poster

import (
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"lobster/internal/httputil"
)

var (
	client = &http.Client{
		Timeout:   10 * time.Second,
		Transport: httputil.NewClient().Transport,
	}
	cache   = make(map[string]string)
	cacheMu sync.RWMutex

	// chafa detection (cached at startup)
	chafaPath  string
	chafaOnce  sync.Once
)

func findChafa() {
	chafaOnce.Do(func() {
		if p, err := exec.LookPath("chafa"); err == nil {
			chafaPath = p
		}
	})
}

// Render downloads the poster at the given URL and returns a line-based
// rendered string for side-by-side layouts.
// width and height are in terminal columns and rows.
func Render(url string, width, height int) string {
	if url == "" || width < 4 || height < 4 {
		return ""
	}

	key := fmt.Sprintf("%s:%d:%d", url, width, height)
	cacheMu.RLock()
	if cached, ok := cache[key]; ok {
		cacheMu.RUnlock()
		return cached
	}
	cacheMu.RUnlock()

	imgPath, cleanup, err := downloadToTemp(url)
	if err != nil {
		return ""
	}
	defer cleanup()

	var result string
	findChafa()
	if chafaPath != "" {
		result = renderChafa(imgPath, width, height)
	}
	if result == "" {
		img, err := decodeFile(imgPath)
		if err != nil {
			return ""
		}
		result = renderHalfBlock(img, width, height)
	}

	cacheMu.Lock()
	cache[key] = result
	cacheMu.Unlock()

	return result
}

// RenderSideBySide renders a poster next to text content.
// Uses chafa when available for best quality, otherwise half-block art.
func RenderSideBySide(url string, posterCols, posterRows int, textLines []string) string {
	if url == "" || posterCols < 4 || posterRows < 4 {
		return strings.Join(textLines, "\n")
	}

	posterStr := Render(url, posterCols, posterRows)
	if posterStr == "" {
		return strings.Join(textLines, "\n")
	}

	posterLines := strings.Split(posterStr, "\n")
	return joinSideBySide(posterLines, posterCols, textLines, 3)
}

// downloadToTemp downloads an image URL to a temp file and returns its path
// and a cleanup function.
func downloadToTemp(url string) (string, func(), error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Determine extension from content type
	ext := ".jpg"
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "png"):
		ext = ".png"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	}

	tmpFile, err := os.CreateTemp("", "lobster-poster-*"+ext)
	if err != nil {
		return "", nil, err
	}

	_, err = io.Copy(tmpFile, io.LimitReader(resp.Body, 5*1024*1024))
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", nil, err
	}

	cleanup := func() { os.Remove(tmpFile.Name()) }
	return tmpFile.Name(), cleanup, nil
}

func decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// renderChafa uses the chafa CLI for high-quality terminal image rendering.
// Uses maximum quality settings: perceptual color space, dithering, and full work level.
func renderChafa(imagePath string, cols, rows int) string {
	args := []string{
		imagePath,
		"--size", fmt.Sprintf("%dx%d", cols, rows),
		"--animate", "off",
		"--format", "symbols",
		"--color-space", "din99d",
		"--dither", "diffusion",
		"--work", "9",
		"--optimize", "0",
	}

	cmd := exec.Command(chafaPath, args...)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimRight(string(out), "\n")
}

// joinSideBySide joins poster lines and text lines horizontally.
func joinSideBySide(posterLines []string, posterWidth int, textLines []string, gap int) string {
	maxLines := len(posterLines)
	if len(textLines) > maxLines {
		maxLines = len(textLines)
	}

	pad := strings.Repeat(" ", posterWidth)
	gapStr := strings.Repeat(" ", gap)

	var sb strings.Builder
	for i := 0; i < maxLines; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		pl := pad
		if i < len(posterLines) {
			pl = posterLines[i]
		}
		sb.WriteString(pl)
		sb.WriteString(gapStr)
		if i < len(textLines) {
			sb.WriteString(textLines[i])
		}
	}

	return sb.String()
}

// renderHalfBlock converts an image to ANSI art using half-block characters.
// Uses CatmullRom (bicubic) downscaling for smooth output.
func renderHalfBlock(img image.Image, cols, rows int) string {
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()

	pixH := rows * 2
	pixW := cols
	if srcW > 0 && srcH > 0 {
		aspect := float64(srcW) / float64(srcH)
		fitW := int(float64(pixH) * aspect)
		if fitW < pixW {
			pixW = fitW
		} else {
			pixH = int(float64(pixW) / aspect)
			if pixH%2 != 0 {
				pixH++
			}
			rows = pixH / 2
		}
	}

	if pixW < 1 || pixH < 1 {
		return ""
	}

	scaled := image.NewRGBA(image.Rect(0, 0, pixW, pixH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)

	var sb strings.Builder
	sb.Grow(pixW * rows * 40)

	for row := 0; row < rows; row++ {
		if row > 0 {
			sb.WriteString("\x1b[0m\n")
		}
		for col := 0; col < pixW; col++ {
			top := scaled.RGBAAt(col, row*2)
			bot := scaled.RGBAAt(col, row*2+1)

			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.R, top.G, top.B,
				bot.R, bot.G, bot.B)
		}
	}
	sb.WriteString("\x1b[0m")

	return sb.String()
}

// TempDir returns the poster cache directory path.
func TempDir() string {
	return filepath.Join(os.TempDir(), "lobster-posters")
}
