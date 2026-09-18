package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
)

// Set by the parent to hand the child the URL its health probe must reach.
const healthProxyChildEnv = "LOBSTER_PROXY_CHILD_HEALTH"

// checkDomainHealth builds its own transport (for the Happy Eyeballs dialer),
// and a custom http.Transport starts with a nil Proxy — meaning "no proxy",
// not "the default". On a network that only reaches the internet through one,
// every domain then probes as dead and failover walks the whole candidate
// list for nothing.
//
// Child process, loopback-only, for the reasons in
// internal/httputil/proxy_test.go: net/http caches HTTP_PROXY once per
// process. The target is a .invalid host (never resolves, RFC 6761) rather
// than a loopback address, because httpproxy exempts localhost from proxying
// and a loopback target would go direct even with the fix in place.
func TestCheckDomainHealthHonoursProxyEnvironment(t *testing.T) {
	if os.Getenv(healthProxyChildEnv) != "" {
		t.Skip("child process: see TestProxyEnvChildHealthProbe")
	}

	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		io.WriteString(w, "ok")
	}))
	defer proxy.Close()

	const target = "http://lobster-proxy-probe.invalid/"
	cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEnvChildHealthProbe$", "-test.v")
	cmd.Env = append(os.Environ(),
		healthProxyChildEnv+"="+target,
		"HTTP_PROXY="+proxy.URL,
		"HTTPS_PROXY="+proxy.URL,
		"NO_PROXY=",
		"no_proxy=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child health probe through HTTP_PROXY failed: %v\n%s", err, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != target {
		t.Fatalf("proxy saw %v, want exactly [%s]; the health probe's transport ignored HTTP_PROXY", seen, target)
	}
}

// The child half. A no-op unless the parent above launched it.
func TestProxyEnvChildHealthProbe(t *testing.T) {
	target := os.Getenv(healthProxyChildEnv)
	if target == "" {
		t.Skip("not the proxy-test child process")
	}

	prev := healthURLFor
	healthURLFor = func(string) string { return target }
	t.Cleanup(func() { healthURLFor = prev })

	if !checkDomainHealth("example.invalid") {
		t.Fatalf("checkDomainHealth = false; with HTTP_PROXY set the probe must go to the proxy, which answers 200, not to the target host")
	}
}
