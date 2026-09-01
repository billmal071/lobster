package provider

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
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

func TestFirstHealthyDomainCachedProbesOnce(t *testing.T) {
	ResetDomainCache()
	t.Cleanup(ResetDomainCache)
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&probes, 1)
	}))
	defer srv.Close()
	pointProbesAt(t, map[string]string{"a.example": srv.URL})
	ov := map[string][]string{"cachedprov": {"a.example"}}

	first := FirstHealthyDomainCached("cachedprov", ov)
	second := FirstHealthyDomainCached("cachedprov", ov)
	if first != "a.example" || second != "a.example" {
		t.Fatalf("got %q then %q, want a.example twice", first, second)
	}
	if n := atomic.LoadInt32(&probes); n != 1 {
		t.Fatalf("probed %d times, want 1 (cached)", n)
	}
}

func TestFirstHealthyDomainCachedCachesMiss(t *testing.T) {
	ResetDomainCache()
	t.Cleanup(ResetDomainCache)
	pointProbesAt(t, nil)
	ov := map[string][]string{"deadprov": {"a.example"}}
	if got := FirstHealthyDomainCached("deadprov", ov); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	// Second call must not re-probe; verify by making probes impossible to
	// distinguish — we just assert it still returns "" instantly.
	start := time.Now()
	if got := FirstHealthyDomainCached("deadprov", ov); got != "" {
		t.Fatalf("second call got %q, want empty", got)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("second call re-probed instead of using the cache")
	}
}

func TestKnownDomainsFlixhqSeeded(t *testing.T) {
	want := []string{"flixhq.to", "flixhq.click", "flixhq.pe", "flixhq.bz", "sflix.to", "myflixerz.to"}
	if !reflect.DeepEqual(knownDomains["flixhq"], want) {
		t.Fatalf("knownDomains[flixhq] = %v, want %v", knownDomains["flixhq"], want)
	}
}
