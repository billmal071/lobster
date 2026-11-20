package provider

import (
	"fmt"
	"testing"
)

func TestIsCatalogueGapNeedsBothSentinels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"both", fmt.Errorf("%w: %w", ErrIncompleteEpisodeList, ErrNotCarried), true},
		{"gap error from the walk", eztvGapError{"x"}, true},
		{"probe-failure prefix", fmt.Errorf("%w: probe 429", ErrIncompleteEpisodeList), false},
		{"not carried alone", ErrNotCarried, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsCatalogueGap(c.err); got != c.want {
				t.Fatalf("IsCatalogueGap = %v, want %v", got, c.want)
			}
		})
	}
}
