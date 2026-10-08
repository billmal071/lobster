package httputil

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// ValidateURL is a library function: it cannot know whether the string it was
// handed carries credentials, so it must not wrap url.Parse's *url.Error, which
// prints that string in full.
func TestValidateURLErrorNeverCarriesTheURL(t *testing.T) {
	const fakeSecret = "notarealpassword"
	raw := "http://notarealuser:" + fakeSecret + "@::1:8080/get.php?token=" + fakeSecret

	err := ValidateURL(raw)
	if err == nil {
		t.Fatalf("ValidateURL(%q) = nil, want a parse failure", raw)
	}
	if msg := err.Error(); strings.Contains(msg, fakeSecret) {
		t.Errorf("ValidateURL error = %q, leaks %q", msg, fakeSecret)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Errorf("ValidateURL error still has a *url.Error in its chain (URL %q)", ue.URL)
	}
}

// CauseWithoutURL is the seam every URL-bearing error has to go through before
// it is wrapped into a message, so it is pinned directly as well as through
// its call sites.
func TestCauseWithoutURL(t *testing.T) {
	const fakeSecret = "notarealpassword"
	raw := "https://notarealuser:" + fakeSecret + "@example.test/x?token=" + fakeSecret
	refused := errors.New("connect: connection refused")

	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil stays nil", nil, nil},
		{"a plain error is returned unchanged", refused, refused},
		{"a *url.Error becomes its cause", &url.Error{Op: "Get", URL: raw, Err: refused}, refused},
		{
			"nested *url.Errors are all stripped",
			&url.Error{Op: "Get", URL: raw, Err: &url.Error{Op: "parse", URL: raw, Err: refused}},
			refused,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CauseWithoutURL(tc.in)
			if got != tc.want {
				t.Fatalf("CauseWithoutURL(%v) = %#v, want %#v", tc.in, got, tc.want)
			}
			if got != nil && strings.Contains(got.Error(), fakeSecret) {
				t.Fatalf("CauseWithoutURL(%v) = %q, leaks %q", tc.in, got, fakeSecret)
			}
		})
	}

	// A *url.Error with no cause must still not come back as itself.
	bare := &url.Error{Op: "Get", URL: raw}
	got := CauseWithoutURL(bare)
	if got == error(bare) {
		t.Fatalf("CauseWithoutURL returned the *url.Error itself for a nil cause")
	}
	if got == nil {
		t.Fatalf("CauseWithoutURL(%v) = nil; a failure must stay a failure", bare)
	}
	if strings.Contains(got.Error(), fakeSecret) {
		t.Fatalf("CauseWithoutURL(%v) = %q, leaks %q", bare, got, fakeSecret)
	}
}
