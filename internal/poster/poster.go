// Package poster downloads and renders poster images for terminal display.
// Supports Kitty graphics protocol for high-quality rendering and falls back
// to half-block ANSI art with bicubic downscaling.
package poster

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"net/http"
	"os"
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
)

// Render downloads the poster at the given URL and returns a line-based
// ANSI string using half-block characters (▀) for side-by-side layouts.
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

	img, err := fetchImage(url)
	if err != nil {
		return ""
	}

	result := renderHalfBlock(img, width, height)

	cacheMu.Lock()
	cache[key] = result
	cacheMu.Unlock()

	return result
}

// RenderSideBySide renders a poster next to text content. Uses Kitty graphics
// protocol for sharp images when supported, otherwise half-block ANSI art.
// posterCols/posterRows control poster size; textLines are printed to the right.
func RenderSideBySide(url string, posterCols, posterRows int, textLines []string) string {
	if url == "" || posterCols < 4 || posterRows < 4 {
		return strings.Join(textLines, "\n")
	}

	img, err := fetchImage(url)
	if err != nil {
		return strings.Join(textLines, "\n")
	}

	if supportsKitty() {
		return renderKittySideBySide(img, posterCols, posterRows, textLines)
	}

	// Half-block fallback: render poster, join side by side
	posterStr := renderHalfBlock(img, posterCols, posterRows)
	if posterStr == "" {
		return strings.Join(textLines, "\n")
	}

	posterLines := strings.Split(posterStr, "\n")
	return joinSideBySide(posterLines, posterCols, textLines, 3)
}

func fetchImage(url string) (image.Image, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	img, _, err := image.Decode(io.LimitReader(resp.Body, 5*1024*1024))
	return img, err
}

// supportsKitty checks if the terminal supports the Kitty graphics protocol.
func supportsKitty() bool {
	term := os.Getenv("TERM")
	termProgram := os.Getenv("TERM_PROGRAM")
	kittyID := os.Getenv("KITTY_WINDOW_ID")

	switch {
	case kittyID != "":
		return true
	case termProgram == "WezTerm":
		return true
	case termProgram == "ghostty":
		return true
	case strings.Contains(term, "kitty"):
		return true
	default:
		return false
	}
}

// renderKittySideBySide renders the poster using Kitty graphics protocol
// and places text to the right using cursor movement escape codes.
func renderKittySideBySide(img image.Image, cols, rows int, textLines []string) string {
	srcW := img.Bounds().Dx()
	srcH := img.Bounds().Dy()

	// Target pixel dimensions (typical cell = 8×16 pixels)
	targetW := cols * 8
	targetH := rows * 16

	// Maintain aspect ratio
	if srcW > 0 && srcH > 0 {
		aspect := float64(srcW) / float64(srcH)
		fitW := int(float64(targetH) * aspect)
		if fitW < targetW {
			targetW = fitW
		} else {
			targetH = int(float64(targetW) / aspect)
		}
	}
	if targetW < 1 || targetH < 1 {
		return strings.Join(textLines, "\n")
	}

	// Scale and encode to PNG
	scaled := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return strings.Join(textLines, "\n")
	}

	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	var sb strings.Builder

	// Emit Kitty graphics escape (chunked)
	first := true
	data := b64
	for len(data) > 0 {
		chunk := data
		more := 0
		if len(chunk) > 4096 {
			chunk = data[:4096]
			data = data[4096:]
			more = 1
		} else {
			data = ""
		}
		if first {
			fmt.Fprintf(&sb, "\x1b_Ga=T,f=100,t=d,c=%d,r=%d,m=%d;%s\x1b\\", cols, rows, more, chunk)
			first = false
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}

	// After the image escape, the cursor is on row 0 at column 0.
	// The image occupies cols×rows cells visually.
	// Print text to the right of the image using cursor positioning.
	gap := 2
	textCol := cols + gap + 1 // 1-indexed column position

	// First text line is on the same row as the image top
	maxLines := rows
	if len(textLines) > maxLines {
		maxLines = len(textLines)
	}

	for i := 0; i < maxLines; i++ {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Move cursor to text column
		if i < rows {
			// We're within the image area — move right past the poster
			fmt.Fprintf(&sb, "\x1b[%dC", textCol-1)
		}
		if i < len(textLines) {
			sb.WriteString(textLines[i])
		}
	}

	// If poster is taller than text, emit empty lines to move past it
	if rows > len(textLines) {
		for i := len(textLines); i < rows; i++ {
			sb.WriteByte('\n')
		}
	}

	return sb.String()
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
