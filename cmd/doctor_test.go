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
	want := []string{"TBCPL", "FlixHQWS", "TBCPL(1shows)", "YTS", "AnimeOnsen", "Nyaa", "EZTV"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("doctorProviders = %v, want %v", got, want)
	}
}
