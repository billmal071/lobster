package provider

import "testing"

func TestNewAllAnimeTranslation(t *testing.T) {
	if got := NewAllAnime(false).Translation(); got != "sub" {
		t.Fatalf("default translation = %q, want sub", got)
	}
	if got := NewAllAnime(true).Translation(); got != "dub" {
		t.Fatalf("dub translation = %q, want dub", got)
	}
	a := NewAllAnime(false)
	a.SetTranslation("dub")
	if got := a.Translation(); got != "dub" {
		t.Fatalf("after SetTranslation = %q, want dub", got)
	}
}

// Compile-time proof the type satisfies the streaming interface.
var _ StreamProvider = (*AllAnime)(nil)
