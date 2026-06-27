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

	// Budget the band's vertical space so it never starves the results list.
	const minListRows = 6
	maxPosterRows := lm.mainHeight - minListRows - 2*bandBorder
	if maxPosterRows < 6 {
		maxPosterRows = 6
	}
	if lm.posterRows > maxPosterRows {
		lm.posterRows = maxPosterRows
		// Re-derive cols from the clamped rows to preserve the image aspect.
		if imgW > 0 && imgH > 0 {
			lm.posterCols = lm.posterRows * imgW * poster.CellAspectDen / (imgH * poster.CellAspectNum)
		} else {
			lm.posterCols = lm.posterRows * 4 / 3
		}
		if lm.posterCols < 15 {
			lm.posterCols = 15
		}
		if lm.posterCols > 40 {
			lm.posterCols = 40
		}
	}

	lm.bandHeight = lm.posterRows + 2*bandBorder

	lm.listWidth = width
	lm.listHeight = lm.mainHeight - lm.bandHeight
	if lm.listHeight < 0 {
		lm.listHeight = 0
	}

	lm.textWidth = width - 2*bandBorder - 2*bandPadH - lm.posterCols - bandGap
	if lm.textWidth < 20 {
		lm.textWidth = 20
	}

	// +1 converts the 0-based offset into a 1-based CUP coordinate.
	lm.bandRow = docMarginV + headerH + tabBarH + bandBorder + 1
	lm.bandCol = docMarginH + bandBorder + bandPadH + 1
	return lm
}

// posterVisible reports whether an inline poster image should currently be
// painted. It must be false whenever anything could cover the hero band.
// posterReady is only set on inline-capable terminals, so no terminal check
// is needed here.
func (m AppModel) posterVisible() bool {
	return m.activeTab != tabDownloads &&
		!m.dlDialog.active &&
		!m.isSearching &&
		m.posterReady
}
