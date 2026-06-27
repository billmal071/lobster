package tui

import "testing"

func TestComputeLayout(t *testing.T) {
	// width/height are already docStyle-frame-stripped (as in WindowSizeMsg).
	m := computeLayout(120, 40, 6, 1, false, 0, 0)

	// poster box from BoxDims(bandCols=120, 0,0): cols=40, rows=30 -> but clamped by band.
	if m.posterCols < 15 || m.posterCols > 40 {
		t.Fatalf("posterCols out of range: %d", m.posterCols)
	}
	// band height = border(1) top + posterRows + border(1) bottom
	if m.bandHeight != m.posterRows+2*bandBorder {
		t.Fatalf("bandHeight=%d want %d", m.bandHeight, m.posterRows+2*bandBorder)
	}
	// list sits below the band; list height = mainHeight - bandHeight
	if m.listHeight != m.mainHeight-m.bandHeight {
		t.Fatalf("listHeight=%d want %d", m.listHeight, m.mainHeight-m.bandHeight)
	}
	if m.listWidth != 120 {
		t.Fatalf("listWidth=%d want 120", m.listWidth)
	}
	// band top-left absolute row = docMarginV + headerH + tabBarH + bandBorder + 1(to 1-based)
	wantRow := docMarginV + 6 + 1 + bandBorder + 1
	if m.bandRow != wantRow {
		t.Fatalf("bandRow=%d want %d", m.bandRow, wantRow)
	}
	// band col = docMarginH + bandBorder + bandPadH + 1(to 1-based)
	wantCol := docMarginH + bandBorder + bandPadH + 1
	if m.bandCol != wantCol {
		t.Fatalf("bandCol=%d want %d", m.bandCol, wantCol)
	}
	// mainHeight reconciles to height - headerH - tabBarH - footerRows
	if m.mainHeight != 40-6-1-footerRows {
		t.Fatalf("mainHeight=%d want %d", m.mainHeight, 40-6-1-footerRows)
	}
}

func TestComputeLayoutSearchShiftsBandRow(t *testing.T) {
	base := computeLayout(120, 40, 6, 1, false, 0, 0)
	srch := computeLayout(120, 40, 6, 1, true, 0, 0)
	if srch.bandRow != base.bandRow+searchHeaderRows {
		t.Fatalf("search bandRow=%d want %d", srch.bandRow, base.bandRow+searchHeaderRows)
	}
}

func TestPosterVisible(t *testing.T) {
	base := AppModel{activeTab: tabMovies, posterReady: true}
	if !base.posterVisible() {
		t.Fatal("expected visible in browse tab with ready poster")
	}

	cases := []struct {
		name string
		mut  func(*AppModel)
	}{
		{"downloads tab hides", func(m *AppModel) { m.activeTab = tabDownloads }},
		{"dialog hides", func(m *AppModel) { m.dlDialog.active = true }},
		{"searching hides", func(m *AppModel) { m.isSearching = true }},
		{"not ready hides", func(m *AppModel) { m.posterReady = false }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base
			c.mut(&m)
			if m.posterVisible() {
				t.Fatalf("%s: expected NOT visible", c.name)
			}
		})
	}
}
