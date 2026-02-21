package subtitle

import (
	"testing"

	"lobster/internal/media"
)

func TestFilter(t *testing.T) {
	subs := []media.Subtitle{
		{Language: "English", Label: "English"},
		{Language: "English", Label: "English - SDH"},
		{Language: "Spanish", Label: "Spanish"},
		{Language: "French", Label: "French"},
	}

	tests := []struct {
		lang     string
		expected int
	}{
		{"english", 2},
		{"spanish", 1},
		{"french", 1},
		{"german", 0},
		{"", 4},
	}

	for _, tt := range tests {
		t.Run(tt.lang, func(t *testing.T) {
			got := Filter(subs, tt.lang)
			if len(got) != tt.expected {
				t.Errorf("Filter(%q) returned %d subs, want %d", tt.lang, len(got), tt.expected)
			}
		})
	}
}

func TestBestMatch(t *testing.T) {
	subs := []media.Subtitle{
		{Language: "English", Label: "English - SDH", URL: "https://example.com/sdh.vtt"},
		{Language: "English", Label: "English", URL: "https://example.com/en.vtt"},
		{Language: "Spanish", Label: "Spanish", URL: "https://example.com/es.vtt"},
	}

	// Should prefer non-SDH English
	best := BestMatch(subs, "english")
	if best == nil {
		t.Fatal("BestMatch returned nil for english")
	}
	if best.Label != "English" {
		t.Errorf("BestMatch preferred %q, want 'English' (non-SDH)", best.Label)
	}

	// Spanish
	best = BestMatch(subs, "spanish")
	if best == nil {
		t.Fatal("BestMatch returned nil for spanish")
	}
	if best.Language != "Spanish" {
		t.Errorf("got language %q, want Spanish", best.Language)
	}

	// No match
	best = BestMatch(subs, "japanese")
	if best != nil {
		t.Error("BestMatch should return nil for unmatched language")
	}
}

func TestTempDir(t *testing.T) {
	tmpDir, err := NewTempDir()
	if err != nil {
		t.Fatalf("NewTempDir() error: %v", err)
	}
	defer tmpDir.Cleanup()

	if tmpDir.path == "" {
		t.Error("temp dir path is empty")
	}
}
