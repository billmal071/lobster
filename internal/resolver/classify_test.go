package resolver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestIsTransient(t *testing.T) {
	transient := []error{
		&net.DNSError{Err: "no such host", Name: "x"},
		context.DeadlineExceeded,
		errors.New("connection reset by peer"),
		fmt.Errorf("server returned status 503"),
		fmt.Errorf("net/http: timeout awaiting response"),
	}
	for _, e := range transient {
		if !isTransient(e) {
			t.Errorf("expected transient: %v", e)
		}
	}
	permanent := []error{
		errors.New("no matching result for \"foo\""),
		fmt.Errorf("server returned status 404"),
		errors.New("failed to parse response"),
	}
	for _, e := range permanent {
		if isTransient(e) {
			t.Errorf("expected permanent: %v", e)
		}
	}
}
