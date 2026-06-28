package resolver

import (
	"context"
	"errors"
	"net"
	"strings"
)

// isTransient reports whether an error is worth one retry. Network/DNS/timeout
// and 5xx-shaped errors are transient; "no match", parse failures, and 4xx
// (incl. 404) are permanent.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, p := range []string{"connection reset", "connection refused", "timeout", "eof", "no route to host", "broken pipe", " 500", " 502", " 503", " 504", "status 5"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
