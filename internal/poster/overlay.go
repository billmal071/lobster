package poster

// CellAspectNum/Den approximate a terminal cell's width:height ratio (~1:2).
const (
	CellAspectNum = 1
	CellAspectDen = 2
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
