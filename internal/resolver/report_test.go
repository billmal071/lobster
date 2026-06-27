package resolver

import (
	"errors"
	"strings"
	"testing"
)

func TestReportSummary(t *testing.T) {
	rep := &Report{Attempts: []Attempt{
		{Provider: "MovieBox", Stage: "match", Err: errors.New("no matching result")},
		{Provider: "Soap2Day", Stage: "resolve", Err: errors.New("status 403")},
	}}
	got := rep.Summary()
	if !strings.Contains(got, "MovieBox: match") || !strings.Contains(got, "Soap2Day: resolve") {
		t.Fatalf("summary missing entries: %q", got)
	}
	if !strings.Contains(got, " · ") {
		t.Fatalf("summary not joined: %q", got)
	}
}
