// Package poster downloads and renders poster images for terminal display.
// Uses iTerm2 inline image protocol for pixel-perfect rendering on supported
// terminals (Warp, iTerm2, WezTerm, mintty), falls back to chafa or
// half-block ANSI art.
package poster

import (
	"context"
	"encoding/base64"
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
	"regexp"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/mattn/go-runewidth"

	"lobster/internal/httputil"
)

// ansiRe matches CSI sequences (\x1b[...X) and OSC sequences (\x1b]...\a or \x1b]...\x1b\\).
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\a\x1b]*(?:\a|\x1b\\)`)

var (
	client = &http.Client{
		Timeout:   10 * time.Second,
		Transport: httputil.NewClient().Transport,
	}
	cache   = make(map[string]string)
	cacheMu sync.RWMutex

	// chafa detection (cached at startup)
	chafaPath string
	chafaOnce sync.Once

	// iTerm2 inline image support detection (cached at startup)
	inlineImageSupported bool
	inlineImageOnce      sync.Once
)

func findChafa() {
	chafaOnce.Do(func() {
		if p, err := exec.LookPath("chafa"); err == nil {
			chafaPath = p
		}
	})
}

// detectInlineImage checks if the terminal supports iTerm2 inline image protocol.
func detectInlineImage() {
	inlineImageOnce.Do(func() {
		tp := os.Getenv("TERM_PROGRAM")
		switch tp {
		case "WarpTerminal", "iTerm.app", "WezTerm", "mintty":
			inlineImageSupported = true
		}
	})
}

// IsInlineImage returns true if the terminal supports pixel-perfect inline images.
// When true, the TUI should stack poster above text instead of side-by-side,
// since the inline image escape sequence can't be joined horizontally by lipgloss.
func IsInlineImage() bool {
	detectInlineImage()
	return inlineImageSupported
}

// upgradeImageURL attempts to get a higher resolution version of the poster.
// For TMDB URLs, replaces small sizes with w780.
func upgradeImageURL(url string) string {
	for _, small := range []string{"/w92/", "/w154/", "/w185/", "/w300/", "/w342/", "/w500/"} {
		if strings.Contains(url, small) {
			return strings.Replace(url, small, "/w780/", 1)
		}
	}
	return url
}

// Render downloads the poster at the given URL and returns a line-based
// rendered string for side-by-side layouts.
// width and height are in terminal columns and rows.
func Render(url string, width, height int) string {
	if url == "" || width < 4 || height < 4 {
		return ""
	}

	url = upgradeImageURL(url)

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

	// Try iTerm2 inline image protocol first (pixel-perfect rendering).
	detectInlineImage()
	if inlineImageSupported {
		result = renderInlineImage(imgPath, width, height)
	}

	// Fall back to chafa symbol art.
	if result == "" {
		findChafa()
		if chafaPath != "" {
			result = renderChafa(imgPath, width, height)
		}
	}

	// Last resort: half-block ANSI art.
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

// RenderTUI renders a poster using only character-art renderers (chafa or
// half-block). Inline images are skipped because they cannot be laid out
// side-by-side with text in a full-screen TUI (bubbletea redraws interfere
// with inline image cursor positioning).
func RenderTUI(url string, width, height int) string {
	if url == "" || width < 4 || height < 4 {
		return ""
	}

	url = upgradeImageURL(url)

	key := fmt.Sprintf("tui:%s:%d:%d", url, width, height)
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
func RenderSideBySide(url string, posterCols, posterRows int, textLines []string) string {
	if url == "" || posterCols < 4 || posterRows < 4 {
		return strings.Join(textLines, "\n")
	}

	posterStr := Render(url, posterCols, posterRows)
	if posterStr == "" {
		return strings.Join(textLines, "\n")
	}

	posterLines := strings.Split(posterStr, "\n")
	return JoinSideBySide(posterLines, posterCols, textLines, 3)
}

// renderInlineImage renders a poster using the iTerm2 inline image protocol.
// This sends the raw image data to the terminal which renders it at full
// pixel resolution within the specified cell dimensions.
func renderInlineImage(imagePath string, cols, rows int) string {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return ""
	}

	b64 := base64.StdEncoding.EncodeToString(data)

	// iTerm2 inline image: \033]1337;File=inline=1;width=Ncols;height=Nrows;preserveAspectRatio=1:<base64>\a
	// Each line of output occupies one row. The image escape spans multiple rows
	// but is a single sequence. We embed it as one line — the terminal handles
	// the vertical space. Remaining rows are filled with empty lines so that
	// side-by-side layout works correctly.
	pad := strings.Repeat(" ", cols)

	var sb strings.Builder
	// First line: image escape + padding so lipgloss measures it as cols wide.
	fmt.Fprintf(&sb, "\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\a%s",
		cols, rows, b64, pad)

	// Remaining rows: spaces so side-by-side join reserves the poster column.
	for i := 1; i < rows; i++ {
		sb.WriteByte('\n')
		sb.WriteString(pad)
	}

	return sb.String()
}

// downloadToTemp downloads an image URL to a temp file and returns its path
// and a cleanup function.
func downloadToTemp(url string) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
func renderChafa(imagePath string, cols, rows int) string {
	args := []string{
		imagePath,
		"--size", fmt.Sprintf("%dx%d", cols, rows),
		"--animate", "off",
		"--format", "symbols",
		"--symbols", "sextant+half+block",
		"--colors", "full",
		"--color-space", "din99d",
		"--color-extractor", "median",
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

// visualWidth returns the visible character count of a string, stripping ANSI escapes.
func visualWidth(s string) int {
	return runewidth.StringWidth(ansiRe.ReplaceAllString(s, ""))
}

// JoinSideBySide joins poster lines and text lines horizontally.
// Accounts for ANSI and OSC escape sequences when aligning columns.
func JoinSideBySide(posterLines []string, posterWidth int, textLines []string, gap int) string {
	maxLines := len(posterLines)
	if len(textLines) > maxLines {
		maxLines = len(textLines)
	}

	gapStr := strings.Repeat(" ", gap)

	var sb strings.Builder
	for i := 0; i < maxLines; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if i < len(posterLines) {
			pl := posterLines[i]
			sb.WriteString(pl)
			// Pad to posterWidth based on visual width, not byte length
			vw := visualWidth(pl)
			if vw < posterWidth {
				sb.WriteString(strings.Repeat(" ", posterWidth-vw))
			}
		} else {
			sb.WriteString(strings.Repeat(" ", posterWidth))
		}
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
