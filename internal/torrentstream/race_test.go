package torrentstream

import (
	"sync"
	"testing"
)

// The entries map is written by Serve and read by every HTTP handler goroutine.
// Run with -race: an unguarded map here is a crash under concurrent playback,
// not a theoretical problem.
func TestServerEntriesAreRaceFree(t *testing.T) {
	s := &Server{entries: make(map[string]*serveEntry)}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune('a' + n%26))
			s.mu.Lock()
			s.entries[key] = &serveEntry{name: key}
			s.mu.Unlock()
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// lookup is the read side handle() uses; exercising it directly keeps
			// the test on the synchronisation rather than on a fake torrent file.
			s.lookup("a")
		}()
	}
	wg.Wait()
}

func TestIsMagnet(t *testing.T) {
	for _, u := range []string{"magnet:?xt=urn:btih:abc", "MAGNET:?xt=x", "  magnet:?x"} {
		if !IsMagnet(u) {
			t.Errorf("%q not recognised as magnet", u)
		}
	}
	for _, u := range []string{"https://x/y.m3u8", "", "http://magnet.example/x"} {
		if IsMagnet(u) {
			t.Errorf("%q wrongly treated as magnet", u)
		}
	}
}
