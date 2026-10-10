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

// The other half of ValidateURL's doc comment, and the reason that comment no
// longer promises credential safety: the error never carries the whole URL, but
// it can quote a short fragment of it, and when a password contains a raw '/',
// '?' or '#' that fragment is the password (or its leading part). net/url cuts
// the authority at the first such character, so the password falls outside the
// userinfo and is read as a port.
//
// Every case asserts on the COMPLETE error string rather than on
// CauseWithoutURL's return value: the leak this file exists for reached users
// through the formatted message, not through a helper, and an assertion on the
// helper is exactly what let the original one through. If net/url's wording
// changes and these fail, ValidateURL's doc comment is the claim to recheck.
func TestValidateURLErrorCanQuoteACredentialFragment(t *testing.T) {
	// Obviously fake credentials throughout; the shape is the point.
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "slash in password",
			raw:  "https://notarealuser:notarealpass/word@example.test/get.php",
			want: `malformed URL: invalid port ":notarealpass" after host`,
		},
		{
			name: "question mark in password",
			raw:  "https://notarealuser:notarealpass?word@example.test/get.php",
			want: `malformed URL: invalid port ":notarealpass" after host`,
		},
		{
			name: "hash in password",
			raw:  "https://notarealuser:notarealpass#word@example.test/get.php",
			want: `malformed URL: invalid port ":notarealpass" after host`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateURL(tc.raw)
			if err == nil {
				t.Fatalf("ValidateURL(%q) = nil, want a parse failure", tc.raw)
			}
			msg := err.Error()
			if msg != tc.want {
				t.Errorf("ValidateURL(%q) error = %q, want %q", tc.raw, msg, tc.want)
			}
			if strings.Contains(msg, tc.raw) {
				t.Errorf("ValidateURL(%q) error = %q, carries the whole URL", tc.raw, msg)
			}
			var ue *url.Error
			if errors.As(err, &ue) {
				t.Errorf("ValidateURL(%q) error still has a *url.Error in its chain (URL %q)", tc.raw, ue.URL)
			}
		})
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
