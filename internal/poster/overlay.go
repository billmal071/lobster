package poster

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"os"
)

// CellAspectNum/Den approximate a terminal cell's width:height ratio (~1:2).
const (
	CellAspectNum = 1
	CellAspectDen = 2
	decsc         = "\x1b7" // save cursor
	decrc         = "\x1b8" // restore cursor
)

// BoxDims returns the poster cell-box size for a band of bandCols columns.
// When imgW or imgH is 0, a 2:3 portrait poster is assumed.
func BoxDims(bandCols, imgW, imgH int) (cols, rows int) {
	cols = bandCols * 35 / 100
	if cols > 40 {
		cols = 40
	}
	if cols < 15 {
		cols = 15
	}
	if imgW > 0 && imgH > 0 {
		// rows = cols * (imgH/imgW) * (cellW/cellH)
		rows = cols * imgH * CellAspectNum / (imgW * CellAspectDen)
	} else {
		rows = cols * 3 / 4
	}
	if rows < 6 {
		rows = 6
	}
	return
}

// inlineImageEscape builds the bare iTerm2 OSC-1337 inline-image sequence.
func inlineImageEscape(cols, rows int, b64 string) string {
	return fmt.Sprintf(
		"\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\a",
		cols, rows, b64)
}

// PositionedImage builds a self-contained sequence that saves the cursor,
// moves to (row,col) [1-based], draws the image, and restores the cursor.
func PositionedImage(row, col, cols, rows int, b64 string) string {
	return decsc + fmt.Sprintf("\x1b[%d;%dH", row, col) +
		inlineImageEscape(cols, rows, b64) + decrc
}

// InlineImageData reads an image file and returns its base64 data and pixel size.
func InlineImageData(imagePath string) (string, int, int, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", 0, 0, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, err
	}
	return base64.StdEncoding.EncodeToString(data), cfg.Width, cfg.Height, nil
}
