package player

import (
	"strings"

	"lobster/internal/lang"
)

// audioLangArgs returns the mpv argument selecting the preferred audio track.
// Without it mpv plays whichever track comes first, which on a multi-dub
// release is routinely not the original language.
func audioLangArgs(pref string) []string {
	list := lang.Aliases(pref)
	if len(list) == 0 {
		return nil
	}
	return []string{"--alang=" + strings.Join(list, ",")}
}

// genericAudioLangArgs returns the audio-language argument for the mpv-frontend
// players, each of which has its own way of forwarding an mpv option.
//
// iina-cli ignores bare mpv flags entirely — passing --alang was a silent
// no-op, so the preference appeared to be set and did nothing. Its documented
// forms are a --mpv- prefix or a -- delimiter; the prefix is used here because
// it keeps the arguments a flat slice. Celluloid takes mpv options through
// --mpv-options instead. An unrecognised player keeps the plain spelling rather
// than guessing at a wrapper that may not exist.
func genericAudioLangArgs(player, pref string) []string {
	list := lang.Aliases(pref)
	if len(list) == 0 {
		return nil
	}
	joined := strings.Join(list, ",")
	switch player {
	case "iina":
		return []string{"--mpv-alang=" + joined}
	case "celluloid":
		return []string{"--mpv-options=--alang=" + joined}
	default:
		return []string{"--alang=" + joined}
	}
}

// vlcAudioLangArgs is the same preference in VLC's spelling.
func vlcAudioLangArgs(pref string) []string {
	list := lang.Aliases(pref)
	if len(list) == 0 {
		return nil
	}
	return []string{"--audio-language=" + strings.Join(list, ",")}
}
