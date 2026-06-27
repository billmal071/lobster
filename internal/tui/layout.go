package tui

import "lobster/internal/poster"

const (
	docMarginV       = 1 // docStyle Margin top
	docMarginH       = 2 // docStyle Margin left
	footerRows       = 3 // footer margin-top + line + spacing
	bandBorder       = 1 // hero band border thickness (top/bottom/left/right)
	bandPadV         = 0
	bandPadH         = 2
	bandGap          = 2 // gap between poster box and detail text within the band
	searchHeaderRows = 3 // search label + input + blank line, when isSearching
)

type layoutMetrics struct {
	mainHeight int
	bandHeight int
	bandRow    int
	bandCol    int
	posterCols int
	posterRows int
	textWidth  int
	listWidth  int
	listHeight int
}

// computeLayout is the single source of truth for browse-view geometry.
// width/height are docStyle-frame-stripped (matching WindowSizeMsg).
func computeLayout(width, height, headerH, tabBarH int, searching bool, imgW, imgH int) layoutMetrics {
	var lm layoutMetrics
	lm.mainHeight = height - headerH - tabBarH - footerRows
	if lm.mainHeight < 0 {
		lm.mainHeight = 0
	}

	lm.posterCols, lm.posterRows = poster.BoxDims(width, imgW, imgH)
	lm.bandHeight = lm.posterRows + 2*bandBorder

	lm.listWidth = width
	lm.listHeight = lm.mainHeight - lm.bandHeight

	lm.textWidth = width - 2*bandBorder - 2*bandPadH - lm.posterCols - bandGap
	if lm.textWidth < 20 {
		lm.textWidth = 20
	}

	searchShift := 0
	if searching {
		searchShift = searchHeaderRows
	}
	// +1 converts the 0-based offset into a 1-based CUP coordinate.
	lm.bandRow = docMarginV + headerH + tabBarH + searchShift + bandBorder + 1
	lm.bandCol = docMarginH + bandBorder + bandPadH + 1
	return lm
}
