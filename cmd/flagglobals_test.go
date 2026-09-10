package cmd

import "testing"

// saveFlagGlobals snapshots every package global that registerPersistentFlags
// binds a flag to, and returns a function that puts them all back. It also
// registers that function with t.Cleanup, so a caller that restores mid-test
// is still covered if it fails before doing so.
//
// pflag writes each flag's registered default into its target variable at
// registration time, so merely building a throwaway command with
// registerPersistentFlags resets all eleven — including flagJSON and
// flagDownload, which mayStreamTorrent reads. A helper that binds the flags
// and does not restore therefore silently unpins whatever the test set up
// beforehand; that stayed invisible only while the pinned values happened to
// equal the defaults.
//
// Callers should restore as soon as they have read what they needed out of
// the parse, not at cleanup time, so the rest of the test body sees the
// values it pinned.
func saveFlagGlobals(t *testing.T) (restore func()) {
	t.Helper()

	dl, lang, alang := flagDownload, flagLanguage, flagAudioLang
	prov, qual, plr, base := flagProvider, flagQuality, flagPlayer, flagBase
	nosubs, cont, js, dbg := flagNoSubs, flagContinue, flagJSON, flagDebug
	restore = func() {
		flagDownload, flagLanguage, flagAudioLang = dl, lang, alang
		flagProvider, flagQuality, flagPlayer, flagBase = prov, qual, plr, base
		flagNoSubs, flagContinue, flagJSON, flagDebug = nosubs, cont, js, dbg
	}
	t.Cleanup(restore)
	return restore
}
