package playlist

import (
	"errors"
	"testing"
)

func TestDecideSurf(t *testing.T) {
	loadErr := errors.New("stream failed to load")
	cases := []struct {
		name     string
		position float64
		err      error
		autoSkip bool
		want     SurfDecision
	}{
		{"mpv load failure -> advance", 0, loadErr, true, SurfAdvance},
		{"mpv played then quit -> menu", 42, nil, true, SurfMenu},
		{"mpv crash after watching -> menu (not a load failure)", 42, loadErr, true, SurfMenu},
		{"mpv clean quit no progress -> menu", 0, nil, true, SurfMenu},
		{"non-mpv error+zero -> menu (no reliable signal)", 0, loadErr, false, SurfMenu},
		{"non-mpv success -> menu", 0, nil, false, SurfMenu},
	}
	for _, c := range cases {
		if got := DecideSurf(c.position, c.err, c.autoSkip); got != c.want {
			t.Errorf("%s: DecideSurf(%v,%v,%v) = %v, want %v", c.name, c.position, c.err, c.autoSkip, got, c.want)
		}
	}
}

func TestNextPrevIndexWrap(t *testing.T) {
	if NextIndex(0, 3) != 1 || NextIndex(2, 3) != 0 {
		t.Fatalf("NextIndex wrap wrong: %d %d", NextIndex(0, 3), NextIndex(2, 3))
	}
	if PrevIndex(0, 3) != 2 || PrevIndex(1, 3) != 0 {
		t.Fatalf("PrevIndex wrap wrong: %d %d", PrevIndex(0, 3), PrevIndex(1, 3))
	}
	if NextIndex(0, 1) != 0 || PrevIndex(0, 1) != 0 {
		t.Fatalf("single-element lineup must stay at 0")
	}
}
