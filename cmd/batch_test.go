package cmd

import (
	"lobster/internal/media"
	"testing"
)

func TestParseEpisodeRange(t *testing.T) {
	episodes := []media.Episode{
		{Number: 1, Title: "Pilot", ID: "101"},
		{Number: 2, Title: "Second", ID: "102"},
		{Number: 3, Title: "Third", ID: "103"},
		{Number: 5, Title: "Fifth", ID: "105"},
		{Number: 8, Title: "Eighth", ID: "108"},
		{Number: 10, Title: "Tenth", ID: "110"},
		{Number: 11, Title: "Eleventh", ID: "111"},
		{Number: 12, Title: "Twelfth", ID: "112"},
	}

	tests := []struct {
		name           string
		input          string
		wantNumbers    []int
		wantErr        bool
		wantEmptyOrErr bool
	}{
		{
			name:        "range 1-5 skips missing episode 4",
			input:       "1-5",
			wantNumbers: []int{1, 2, 3, 5},
		},
		{
			name:        "comma-separated list",
			input:       "3,8,10",
			wantNumbers: []int{3, 8, 10},
		},
		{
			name:        "mixed ranges and singles",
			input:       "1-3,10-12",
			wantNumbers: []int{1, 2, 3, 10, 11, 12},
		},
		{
			name:        "range exceeding max returns all existing episodes",
			input:       "1-20",
			wantNumbers: []int{1, 2, 3, 5, 8, 10, 11, 12},
		},
		{
			name:        "single episode",
			input:       "5",
			wantNumbers: []int{5},
		},
		{
			name:    "empty input errors",
			input:   "",
			wantErr: true,
		},
		{
			name:    "non-numeric input errors",
			input:   "abc",
			wantErr: true,
		},
		{
			name:           "episode 0 does not exist",
			input:          "0",
			wantEmptyOrErr: true,
		},
		{
			name:        "range and single combined",
			input:       "1-3,8",
			wantNumbers: []int{1, 2, 3, 8},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseEpisodeRange(tt.input, episodes)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tt.input)
				}
				return
			}

			if tt.wantEmptyOrErr {
				if err == nil && len(result) != 0 {
					t.Fatalf("expected error or empty result for input %q, got %d episodes", tt.input, len(result))
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tt.input, err)
			}

			if len(result) != len(tt.wantNumbers) {
				t.Fatalf("expected %d episodes, got %d", len(tt.wantNumbers), len(result))
			}

			for i, ep := range result {
				if ep.Number != tt.wantNumbers[i] {
					t.Errorf("result[%d]: expected episode %d, got %d", i, tt.wantNumbers[i], ep.Number)
				}
			}
		})
	}
}
