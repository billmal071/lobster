package cmd

import (
	"errors"
	"strings"
	"testing"

	"lobster/internal/config"
)

func TestFormatStreamResolveConcise(t *testing.T) {
	got := formatStreamResolveConcise("Devil May Cry S01E08", []providerAttempt{
		{Name: "Soap2Day", Role: "fallback", Err: errors.New("no embed ID in video URL")},
		{Name: "MovieBox", Role: "fallback", Err: errors.New("moviebox watch: unexpected status 407 from https://api6.aoneroom.com/...")},
		{Name: "FlixHQ", Role: "fallback", Err: errors.New("search failed: unexpected status 522")},
	})

	if !strings.Contains(got, "Devil May Cry S01E08") {
		t.Fatalf("missing episode: %q", got)
	}
	if !strings.Contains(got, "Soap2Day: no source") {
		t.Fatalf("missing soap2day summary: %q", got)
	}
	if !strings.Contains(got, "MovieBox: HTTP 407") {
		t.Fatalf("missing moviebox summary: %q", got)
	}
	if !strings.Contains(got, "FlixHQ: HTTP 522") {
		t.Fatalf("missing flixhq summary: %q", got)
	}
	if strings.Contains(got, "Providers tried:") {
		t.Fatalf("should be concise, got verbose block: %q", got)
	}
	if !strings.Contains(got, "Use -x for details") {
		t.Fatalf("expected -x hint when not in debug: %q", got)
	}
}

func TestStreamResolveErrorConciseWithDebug(t *testing.T) {
	cfg = config.Default()
	cfg.Debug = true
	t.Cleanup(func() { cfg = nil })

	err := newStreamResolveError("Show S01E01", []providerAttempt{
		{Name: "MovieBox", Role: "primary", Err: errors.New("unexpected status 407")},
	})
	got := err.Error()
	if strings.Contains(got, "Use -x") {
		t.Fatalf("debug mode should not suggest -x again: %q", got)
	}
	if !strings.Contains(got, "MovieBox: HTTP 407") {
		t.Fatalf("expected short summary: %q", got)
	}
}

func TestShortenResolveErr(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"no embed ID in video URL", "no source"},
		{"unexpected status 407 from host", "HTTP 407"},
		{"lookup kimcartoon.li: no such host", "DNS error"},
	}
	for _, tc := range cases {
		got := shortenResolveErr(errors.New(tc.in))
		if got != tc.want {
			t.Fatalf("shortenResolveErr(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
