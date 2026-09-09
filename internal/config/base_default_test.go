package config

import "testing"

// The default Base is what every invocation uses when there is no config.toml
// and no --base, so it has to be a source that can actually answer.
//
// flixhq.ws cannot. Measured 2026-09-09 against Marvel's Agents of
// S.H.I.E.L.D. (tv/1403, 22 episodes in season 1), `episodes --season 1`
// returned HTTP 404 from flixhq.ws while soap2day, vaplayer and tbcpl each
// returned the correct 22 — so with no config.toml on disk the default
// invocation was pointed at a source that cannot enumerate a series at all.
//
// The replacement is the sentinel "auto", which cmd/provider.go maps to a live
// general-purpose source and cmd/typeroute.go uses as the signal that the user
// has expressed no preference, so lobster may pick per content type. Any
// explicit value — flag or config file — is left alone.
func TestDefaultBaseIsAuto(t *testing.T) {
	got := Default().Base
	if got == "flixhq.ws" {
		t.Fatalf("Base default is still %q, the source measured 404ing on TV season enumeration", got)
	}
	if got != "auto" {
		t.Fatalf("Base default = %q, want \"auto\"", got)
	}
}

// "auto" must survive Validate, which rejects an empty base.
func TestDefaultConfigWithAutoBaseValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v, want nil", err)
	}
}
