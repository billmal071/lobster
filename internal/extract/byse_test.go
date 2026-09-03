package extract

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// The direct weneverbeenfree.com URL above never reaches resolveCode's
// redirect request, so it alone would not catch that request losing its
// context. Here the embed URL is a non-Byse host, forcing the redirect
// request; the server holds the response open until the context is canceled
// mid-request, and cancellation must abort it.
func TestByseExtractContextCancelsRedirectRequest(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // hold the response until the client gives up
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	errCh := make(chan error, 1)
	go func() {
		_, err := NewByse().ExtractContext(ctx, srv.URL+"/e/abc123", "1080")
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled in its chain", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ExtractContext did not return after cancellation; the redirect request ignores its context")
	}
}
