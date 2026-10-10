package httputil

import (
	"errors"
	"net/url"
)

// CauseWithoutURL returns the diagnosis inside err with every *url.Error
// stripped off, so the result can be wrapped into a message without putting a
// URL back into it.
//
// url.Parse, http.NewRequest, http.Client.Do and their relatives all fail with
// a *url.Error, and its Error method prints the URL it was handed — userinfo,
// query string and all. Wrapping one with %w therefore re-exposes the whole
// URL inside a message that may have gone to some trouble to print only a
// redacted form of it, and leaves it reachable by errors.As for anything
// downstream that formats the cause. The underlying cause carries what the
// reader actually needs (`invalid port ":80x80" after host`, `connection
// refused`) and, as of Go 1.27, never more than a short fragment of the input:
// a host, a port, or an escape sequence.
//
// That fragment is not guaranteed to be harmless, though. net/url finds the
// authority by cutting the input at the first '/', '?' or '#', so a password
// containing one of those characters raw ends up outside the userinfo and is
// reported as the offending port: url.Parse("http://u:pa?ss@host") fails with
// `invalid port ":pa" after host`. Stripping the *url.Error is therefore
// necessary but not sufficient — a caller holding a credential still has to
// decide whether to print the cause at all, as provider.LiveTV.httpGet does
// when redaction cannot take its source apart.
//
// It unwraps through intermediate wrappers, so it discards any message text
// above the URL error as well. That makes it a thing to apply to an error on
// its way out of net/url or net/http, before it is wrapped — not to an error
// that already carries context worth keeping.
//
// A *url.Error with no cause at all would otherwise be returned unchanged,
// taking its URL with it, so it degrades to its Op instead.
func CauseWithoutURL(err error) error {
	for {
		var ue *url.Error
		if !errors.As(err, &ue) {
			return err
		}
		if ue.Err == nil {
			if ue.Op == "" {
				return errors.New("request failed")
			}
			return errors.New(ue.Op + " failed")
		}
		err = ue.Err
	}
}
