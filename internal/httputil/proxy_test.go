package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// The environment variable that turns this test binary into the child half of
// the proxy test, carrying the URL the child must fetch.
const proxyChildTargetEnv = "LOBSTER_PROXY_CHILD_TARGET"

// TestNewClientHonoursProxyEnvironment drives a real request through a real
// proxy and asserts the proxy saw it. Asserting that Transport.Proxy is
// non-nil would only restate the source line; it would not catch a client
// that never reaches its transport.
//
// It runs in a child process because net/http reads HTTP_PROXY exactly once
// per process (envProxyOnce in net/http/transport.go) and caches the result,
// so a t.Setenv here would be silently ignored the moment any earlier test in
// this package had already made a request through a proxied transport. The
// child is this same test binary with -test.run pinned to the child case.
//
// Nothing leaves the machine: the proxy is an httptest server on loopback,
// and the target is a .invalid host, which RFC 6761 guarantees never resolves
// — so when the proxy is skipped the request fails at once instead of
// reaching anything. It cannot be a loopback address: httpproxy's rules
// exempt localhost from proxying, so 127.0.0.1 would go direct even with the
// fix in place and the test would never be able to fail.
func TestNewClientHonoursProxyEnvironment(t *testing.T) {
	if os.Getenv(proxyChildTargetEnv) != "" {
		t.Skip("child process: see TestProxyEnvChildFetch")
	}

	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()

	const target = "http://lobster-proxy-probe.invalid/probe"
	cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEnvChildFetch$", "-test.v")
	cmd.Env = append(os.Environ(),
		proxyChildTargetEnv+"="+target,
		"HTTP_PROXY="+proxy.URL,
		"HTTPS_PROXY="+proxy.URL,
		"NO_PROXY=",
		"no_proxy=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child fetch through HTTP_PROXY failed: %v\n%s", err, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != target {
		t.Fatalf("proxy saw %v, want exactly [%s]; NewClient's transport ignored HTTP_PROXY", seen, target)
	}
}

// The child half. It is a no-op unless the parent above launched it.
func TestProxyEnvChildFetch(t *testing.T) {
	target := os.Getenv(proxyChildTargetEnv)
	if target == "" {
		t.Skip("not the proxy-test child process")
	}

	resp, err := NewClient().Get(target)
	if err != nil {
		t.Fatalf("Get(%s): %v; with HTTP_PROXY set this must go to the proxy, not to the target host", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if strings.TrimSpace(string(body)) != "via-proxy" {
		t.Fatalf("body = %q, want %q", body, "via-proxy")
	}
}
