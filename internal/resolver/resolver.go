package resolver

import (
	"errors"
	"net/http"
	"time"

	"lobster/internal/media"
	"lobster/internal/provider"
)

var errEmptyProviderSet = errors.New("no fallback providers")

// Resolver holds the shared configuration used by probe and the racing Resolve
// method (added in Task 9).
type Resolver struct {
	health         *HealthStore
	client         *http.Client
	batchSize      int
	attemptTimeout time.Duration
	overallTimeout time.Duration
	log            func(string, ...any)
	validate       bool
}

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
