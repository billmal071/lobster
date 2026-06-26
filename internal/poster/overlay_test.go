package poster

import "testing"

func TestBoxDims(t *testing.T) {
	cases := []struct {
		name                   string
		bandCols, imgW, imgH   int
		wantCols, wantRows     int
	}{
		{"default aspect when no dims", 100, 0, 0, 35, 26}, // cols=clamp(35,15,40)=35; rows=35*3/4=26
		{"clamp cols to 40 max", 200, 0, 0, 40, 30},        // 200*35/100=70 -> 40; rows=40*3/4=30
		{"clamp cols to 15 min", 20, 0, 0, 15, 11},         // 20*35/100=7 -> 15; rows=15*3/4=11
		{"portrait 2:3 image", 100, 600, 900, 35, 26},      // rows=35*900/(600*2)=26
		{"square image", 100, 500, 500, 35, 17},            // rows=35*500/(500*2)=17
		{"rows floor at 6", 20, 100, 10, 15, 6},            // rows=15*10/(100*2)=0 -> 6
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cols, rows := BoxDims(c.bandCols, c.imgW, c.imgH)
			if cols != c.wantCols || rows != c.wantRows {
				t.Fatalf("BoxDims(%d,%d,%d)=(%d,%d) want (%d,%d)",
					c.bandCols, c.imgW, c.imgH, cols, rows, c.wantCols, c.wantRows)
			}
		})
	}
}
