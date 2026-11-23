package cmd

import (
	"fmt"
	"testing"

	"lobster/internal/provider"
)

func TestDoctorProbesExactlyTheReachableSources(t *testing.T) {
	var got []string
	for _, n := range doctorProviders() {
		got = append(got, n.Name)
		if _, ok := n.Provider.(*provider.VaPlayer); ok {
			t.Errorf("doctor probes VaPlayer under %q, a retired source", n.Name)
		}
	}
	want := []string{"TBCPL", "FlixHQWS", "TBCPL(1shows)", "Vidrock", "YTS", "AnimeOnsen", "AnimeHeaven", "Nyaa", "EZTV", "1337x"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("doctorProviders = %v, want %v", got, want)
	}
}
