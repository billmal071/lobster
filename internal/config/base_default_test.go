package config

import "testing"

// The default Base is what every invocation uses when there is no config.toml
// and no --base. It is the sentinel "auto", not a named source: cmd/provider.go
// maps it to the general-purpose source (flixhq.ws), and cmd/typeroute.go reads
// it as "no preference expressed", so lobster may pick per content type (films
// to YTS where it has them, since YTS has no TV catalogue). Any explicit
// value — flag or config file — is left alone.
//
// flixhq.ws returned HTTP 404 on TV season listing on 2026-09-09; on
// 2026-11-13 one show (Severance) listed its season through it again. That is
// a single measurement, so this test pins "auto", not the source behind it.
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
