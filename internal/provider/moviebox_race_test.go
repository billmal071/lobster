package provider

import (
	"sync"
	"testing"
)

func TestMovieBoxRotateHostRace(t *testing.T) {
	m := NewMovieBox()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = m.baseURL() }()
		go func() { defer wg.Done(); m.rotateHost() }()
	}
	wg.Wait()
}
