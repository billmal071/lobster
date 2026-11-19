package provider

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"
)

func TestVidrockCheckURLRefusesLocalLiteralsAndZones(t *testing.T) {
	v := NewVidrock()
	for _, raw := range []string{
		"https://[fe80::1%25eth0]/master.m3u8",
		"https://[::1%25lo]/master.m3u8",
		"https://[::ffff:127.0.0.1]/m.m3u8",
		"https://[::ffff:10.0.0.1]/m.m3u8",
		"https://100.64.0.1/m.m3u8",
		"https://224.0.0.1/m.m3u8",
		"https://[ff02::1]/m.m3u8",
		"https://0.0.0.0/m.m3u8",
	} {
		if err := v.checkURL(raw); err == nil {
			t.Errorf("checkURL(%q) accepted a local address", raw)
		}
	}
	if err := v.checkURL("https://8.8.8.8/m.m3u8"); err != nil {
		t.Errorf("public literal refused: %v", err)
	}
}

type vrDialRecorder struct {
	mu    sync.Mutex
	addrs []string
}

func (r *vrDialRecorder) dial(inner func(string) string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		r.mu.Lock()
		r.addrs = append(r.addrs, addr)
		r.mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, inner(addr))
	}
}

func vrGuarded(t *testing.T, hosts map[string]string, target string) (*Vidrock, *vrDialRecorder) {
	t.Helper()
	rec := &vrDialRecorder{}
	v := NewVidrock()
	v.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		ip, ok := hosts[host]
		if !ok {
			return nil, fmt.Errorf("no such host %s", host)
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
	_, port, _ := net.SplitHostPort(target)
	v.dial = rec.dial(func(string) string { return net.JoinHostPort("127.0.0.1", port) })
	v.proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
	return v, rec
}

func vrTLSServer(t *testing.T, h http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

func TestVidrockRefusesAHostnameThatResolvesToALocalAddress(t *testing.T) {
	var hits int
	srv, pool := vrTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	for _, ip := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "100.64.0.9", "::1", "::ffff:127.0.0.1", "0.0.0.0", "224.0.0.1"} {
		v, rec := vrGuarded(t, map[string]string{"example.com": ip}, srv.Listener.Addr().String())
		v.tlsRoots = pool
		_, _, _, err := v.fetchPlaylist(context.Background(), "A", "https://example.com:"+portOf(srv)+"/m.m3u8")
		if err == nil {
			t.Errorf("resolving to %s was fetched", ip)
		}
		if len(rec.addrs) != 0 {
			t.Errorf("resolving to %s was dialled: %v", ip, rec.addrs)
		}
	}
	if hits != 0 {
		t.Fatalf("server hit %d times", hits)
	}
}

func portOf(s *httptest.Server) string {
	_, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	return p
}

func TestVidrockDialsAHostnameThatResolvesToAPublicAddress(t *testing.T) {
	srv, pool := vrTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "#EXTM3U\n") }))
	v, rec := vrGuarded(t, map[string]string{"example.com": "93.184.216.34"}, srv.Listener.Addr().String())
	v.tlsRoots = pool
	text, _, _, err := v.fetchPlaylist(context.Background(), "A", "https://example.com:"+portOf(srv)+"/m.m3u8")
	if err != nil || text != "#EXTM3U\n" {
		t.Fatalf("got %q, %v", text, err)
	}
	if len(rec.addrs) != 1 || rec.addrs[0] != "93.184.216.34:"+portOf(srv) {
		t.Fatalf("dialled %v, want the resolved public address", rec.addrs)
	}
}

func TestVidrockRefusesARedirectToAHostnameThatResolvesLocally(t *testing.T) {
	var internalHits int
	var srv *httptest.Server
	var pool *x509.CertPool
	srv, pool = vrTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "internal.test:"+portOf(srv) {
			internalHits++
			return
		}
		http.Redirect(w, r, "https://internal.test:"+portOf(srv)+"/x.m3u8", http.StatusFound)
	}))
	v, rec := vrGuarded(t, map[string]string{"example.com": "93.184.216.34", "internal.test": "10.0.0.5"}, srv.Listener.Addr().String())
	v.tlsRoots = pool
	if _, _, _, err := v.fetchPlaylist(context.Background(), "A", "https://example.com:"+portOf(srv)+"/m.m3u8"); err == nil {
		t.Fatal("redirect to a local-resolving host was followed")
	}
	if internalHits != 0 {
		t.Fatalf("internal host reached %d times", internalHits)
	}
	for _, a := range rec.addrs {
		if a != "93.184.216.34:"+portOf(srv) {
			t.Fatalf("dialled %s", a)
		}
	}
}

func TestVidrockAllowLocalLeavesTheTransportUnguarded(t *testing.T) {
	v := NewVidrock()
	if v.httpClient().Transport == nil {
		t.Fatal("default client should be dial-guarded")
	}
	v.allowLocal = true
	if v.httpClient().Transport != nil {
		t.Fatal("allowLocal client should keep the default transport")
	}
}

func TestVidrockProxiedRequestsAreNotDialGuarded(t *testing.T) {
	var mu sync.Mutex
	var connects int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodConnect {
			connects++
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	v, rec := vrGuarded(t, nil, "127.0.0.1:1")
	v.proxy = func(*http.Request) (*url.URL, error) { return url.Parse(proxy.URL) }
	if _, _, _, err := v.fetchPlaylist(context.Background(), "A", "https://example.com/m.m3u8"); err == nil {
		t.Fatal("want an error from the failing proxy")
	}
	mu.Lock()
	defer mu.Unlock()
	if connects != 1 || len(rec.addrs) != 0 {
		t.Fatalf("proxy CONNECTs %d, guarded dials %v; the proxy should be reached directly", connects, rec.addrs)
	}
}
