package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// pointProbesAt makes every probed "domain" hit the given handler by rewriting
// the probe URL. The domain string itself is ignored except when it matches a
// key in routes, which maps domain -> httptest server URL. Unrouted domains
// get an unreachable address so they fail fast.
func pointProbesAt(t *testing.T, routes map[string]string) {
	t.Helper()
	old := healthURLFor
	healthURLFor = func(domain string) string {
		if u, ok := routes[domain]; ok {
			return u
		}
		return "http://127.0.0.1:1/" // closed port: immediate refusal
	}
	t.Cleanup(func() { healthURLFor = old })
}

func TestFirstHealthyDomainPrefersListOrder(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	pointProbesAt(t, map[string]string{"b.example": ok.URL, "c.example": ok.URL})

	got := FirstHealthyDomain("testprov", map[string][]string{
		"testprov": {"a.example", "b.example", "c.example"},
	})
	if got != "b.example" {
		t.Fatalf("got %q, want b.example (first healthy in preference order)", got)
	}
}

func TestFirstHealthyDomainAllDeadReturnsEmpty(t *testing.T) {
	pointProbesAt(t, nil)
	if got := FirstHealthyDomain("testprov", map[string][]string{"testprov": {"a.example", "b.example"}}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestFirstHealthyDomainHangingProbeDoesNotBlock(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer hang.Close()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	pointProbesAt(t, map[string]string{"hang.example": hang.URL, "ok.example": ok.URL})

	oldTimeout := probeTimeout
	probeTimeout = 500 * time.Millisecond
	t.Cleanup(func() { probeTimeout = oldTimeout })

	start := time.Now()
	got := FirstHealthyDomain("testprov", map[string][]string{
		"testprov": {"hang.example", "ok.example"},
	})
	elapsed := time.Since(start)
	if got != "ok.example" {
		t.Fatalf("got %q, want ok.example", got)
	}
	// Parallel: total time ~ one probe timeout, not the sum of probes.
	if elapsed > 2*time.Second {
		t.Fatalf("gate took %s; probes are not parallel or timeout ignored", elapsed)
	}
}

func TestFirstHealthyDomainUsesKnownDomains(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	oldKnown := knownDomains["_fhd_test"]
	knownDomains["_fhd_test"] = []string{"known.example"}
	t.Cleanup(func() {
		if oldKnown == nil {
			delete(knownDomains, "_fhd_test")
		} else {
			knownDomains["_fhd_test"] = oldKnown
		}
	})
	pointProbesAt(t, map[string]string{"known.example": ok.URL})

	if got := FirstHealthyDomain("_fhd_test", nil); got != "known.example" {
		t.Fatalf("got %q, want known.example", got)
	}
}
