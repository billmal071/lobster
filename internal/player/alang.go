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

// vlcAudioLangArgs is the same preference in VLC's spelling.
func vlcAudioLangArgs(pref string) []string {
	list := lang.Aliases(pref)
	if len(list) == 0 {
		return nil
	}
	return []string{"--audio-language=" + strings.Join(list, ",")}
}
