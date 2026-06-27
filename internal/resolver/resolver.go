package resolver

import (
	"errors"

	"lobster/internal/media"
	"lobster/internal/provider"
)

var errEmptyProviderSet = errors.New("no fallback providers")

// ResolveSequential is a temporary sequential entry point used while the
// call sites still live in cmd. Task 9 replaces its body with the racing Resolver.
func ResolveSequential(providers []provider.Provider, req Request, log func(string, ...any)) (*media.Stream, error) {
	var lastErr error
	for _, p := range providers {
		s, _, err := resolveWithProvider(p, req, log)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errEmptyProviderSet
	}
	return nil, lastErr
}
