package resolver

import (
	"fmt"
	"strings"
)

// Report records every probe attempt made during a Resolve call.
type Report struct {
	Attempts []Attempt
}

// Attempt is a single probe result recorded in a Report.
type Attempt struct {
	Provider   string
	Stage      string
	Err        error
	DurationMs int64
}

// Summary renders a compact one-line-per-provider failure digest.
func (rep *Report) Summary() string {
	parts := make([]string, 0, len(rep.Attempts))
	for _, a := range rep.Attempts {
		msg := "ok"
		if a.Err != nil {
			msg = a.Err.Error()
		}
		stage := a.Stage
		if stage == "" {
			stage = "?"
		}
		parts = append(parts, fmt.Sprintf("%s: %s %s", a.Provider, stage, msg))
	}
	return strings.Join(parts, " · ")
}
