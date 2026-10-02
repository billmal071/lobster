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

// SyntheticProvider is the Provider name recorded for an attempt that is not a
// provider probe at all, but a note the resolver made about itself: Resolve
// appends one when its own overall deadline expires, so the report says why it
// stopped rather than ending on whatever the last provider happened to do.
//
// It is a constant with a predicate beside it, rather than a string every
// reader compares against, because consumers outside this package count
// providers. cmd's resolveFailure.providersTried feeds "%d providers tried"
// into a user-facing failure message; comparing on the stage string there put
// a cross-package literal between the producer and the count, so renaming the
// stage here would have silently inflated that number by one. The producer and
// the predicate now live together, and
// TestResolveOverallTimeoutRowIsNotAProviderProbe pins them in sync.
const SyntheticProvider = "(resolver)"

// StageOverallTimeout is the Stage on the SyntheticProvider attempt Resolve
// appends when its overall deadline expires.
const StageOverallTimeout = "overall-timeout"

// IsProviderProbe reports whether a records a real provider being asked for a
// stream. Callers that count or rank providers must skip the attempts where it
// is false; callers that render the attempts for a reader should keep them,
// since "the resolver ran out of time" is the most actionable row in the list.
func (a Attempt) IsProviderProbe() bool { return a.Provider != SyntheticProvider }

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
