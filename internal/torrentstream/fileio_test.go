package torrentstream

import (
	"strings"
	"testing"
)

// Reproducing a truncate/mmap race is inherently flaky, so what is pinned here
// is the decision rather than the race: given an environment, do we re-exec,
// and with what?
func TestPlanFileIo(t *testing.T) {
	cases := []struct {
		name       string
		willStream bool
		set        bool
		current    string
		canExec    bool
		want       ioPlan
	}{{
		name:       "a run that cannot open a magnet is left alone",
		willStream: false, set: false, canExec: true,
		want: ioPlan{},
	}, {
		name:       "an unset backend on a streaming run is switched to classic",
		willStream: true, set: false, canExec: true,
		want: ioPlan{exec: true, value: classicIo},
	}, {
		// Overriding this would make the user's own setting a lie, and picking
		// throughput over the race is theirs to choose.
		name:       "an explicit mmap choice is respected, race and all",
		willStream: true, set: true, current: "mmap", canExec: true,
		want: ioPlan{},
	}, {
		name:       "an explicit classic choice needs no second process",
		willStream: true, set: true, current: classicIo, canExec: true,
		want: ioPlan{},
	}, {
		// The library panics in its own init() on an unrecognised value, before
		// any of our code runs, so this process would already be dead. What
		// matters is that we never re-exec on top of it and never pass it on.
		name:       "a set-but-unrecognised value is still the user's setting",
		willStream: true, set: true, current: "mmapp", canExec: true,
		want: ioPlan{},
	}, {
		name:       "without exec the user is told how to opt in",
		willStream: true, set: false, canExec: false,
		want: ioPlan{warn: "..."},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := planFileIo(c.willStream, c.set, c.current, c.canExec)
			if c.want.warn != "" {
				if got.warn == "" {
					t.Fatalf("planFileIo returned no warning: %+v", got)
				}
				if got.exec {
					t.Errorf("planFileIo wants to re-exec on a platform that cannot")
				}
				// A warning nobody can act on is noise.
				if !strings.Contains(got.warn, fileIoEnv) || !strings.Contains(got.warn, classicIo) {
					t.Errorf("the warning does not say what to set: %q", got.warn)
				}
				return
			}
			if got != c.want {
				t.Errorf("planFileIo(%v, %v, %q, %v) = %+v, want %+v",
					c.willStream, c.set, c.current, c.canExec, got, c.want)
			}
		})
	}
}

// The re-exec must never target anything but the backend it promises. Passing
// an unrecognised value would panic the new process in the library's init,
// replacing a narrow race with a certain crash.
func TestPlanFileIoOnlyEverSelectsClassic(t *testing.T) {
	plan := planFileIo(true, false, "", true)
	if !plan.exec {
		t.Fatalf("expected a re-exec, got %+v", plan)
	}
	if plan.value != classicIo {
		t.Errorf("re-exec would set %s=%q; the library panics in init() on anything but %q or \"mmap\"",
			fileIoEnv, plan.value, classicIo)
	}
}

// ensureClassicFileIo must stay silent when there is nothing to do, or a user
// who has already chosen gets nagged on every run.
func TestEnsureClassicFileIoIsSilentWhenTheBackendIsChosen(t *testing.T) {
	t.Setenv(fileIoEnv, classicIo)
	var said []string
	ensureClassicFileIo(true, func(f string, a ...any) { said = append(said, f) })
	if len(said) != 0 {
		t.Errorf("ensureClassicFileIo said %v with the backend already chosen", said)
	}
}

func TestEnsureClassicFileIoIsSilentForANonStreamingRun(t *testing.T) {
	t.Setenv(fileIoEnv, "")
	var said []string
	ensureClassicFileIo(false, func(f string, a ...any) { said = append(said, f) })
	if len(said) != 0 {
		t.Errorf("ensureClassicFileIo said %v for a run that cannot open a magnet", said)
	}
}
