package player

import "testing"

// iina-cli does not forward bare mpv options: --alang is simply ignored, so the
// preference silently did nothing on IINA. The documented forms are a --mpv-
// prefix or a -- delimiter; the prefix keeps the args a flat slice.
func TestGenericAudioLangUsesMpvPrefixForIINA(t *testing.T) {
	got := genericAudioLangArgs("iina", "english")
	if len(got) != 1 || got[0] != "--mpv-alang=eng,en,english" {
		t.Errorf("iina args = %v, want [--mpv-alang=eng,en,english]", got)
	}
}

// Celluloid takes mpv options through --mpv-options, not as bare flags.
func TestGenericAudioLangWrapsForCelluloid(t *testing.T) {
	got := genericAudioLangArgs("celluloid", "english")
	if len(got) != 1 || got[0] != "--mpv-options=--alang=eng,en,english" {
		t.Errorf("celluloid args = %v, want [--mpv-options=--alang=eng,en,english]", got)
	}
}

func TestGenericAudioLangEmptyPreferenceAddsNothing(t *testing.T) {
	for _, p := range []string{"iina", "celluloid"} {
		if got := genericAudioLangArgs(p, ""); len(got) != 0 {
			t.Errorf("%s with no preference = %v, want none", p, got)
		}
	}
}

// An unknown generic player keeps the plain mpv spelling rather than guessing
// at a wrapper that may not exist.
func TestGenericAudioLangUnknownPlayerUsesPlainFlag(t *testing.T) {
	got := genericAudioLangArgs("someplayer", "english")
	if len(got) != 1 || got[0] != "--alang=eng,en,english" {
		t.Errorf("unknown player args = %v, want the plain --alang form", got)
	}
}
