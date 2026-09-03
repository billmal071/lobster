package extract

import (
	"context"
	"errors"
	"testing"
)

// ExtractContext exists so an upstream watch budget bounds stream resolution.
// A context that is already done must abort before any request is issued —
// this also guarantees the test never touches the live network.
func TestByseExtractContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewByse().ExtractContext(ctx, "https://weneverbeenfree.com/e/abc123", "1080")
	if err == nil {
		t.Fatal("expected an error from a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled in its chain", err)
	}
}
