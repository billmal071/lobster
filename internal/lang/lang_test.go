package lang

import "testing"

// The player and the provider each had their own table, and they drifted: -a
// russian set mpv's --alang but left Vidzee's Hindi-first source order alone,
// because only one of the two maps knew "rus". One table, or it happens again.
func TestAliasesOrderedMostSpecificFirst(t *testing.T) {
	got := Aliases("english")
	want := []string{"eng", "en", "english"}
	if len(got) != len(want) {
		t.Fatalf("Aliases(english) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Aliases(english) = %v, want %v", got, want)
		}
	}
}

func TestAliasesUnknownPassesThrough(t *testing.T) {
	if got := Aliases("klingon"); len(got) != 1 || got[0] != "klingon" {
		t.Errorf("Aliases(klingon) = %v, want [klingon]", got)
	}
}

func TestAliasesEmptyIsNone(t *testing.T) {
	if got := Aliases(""); len(got) != 0 {
		t.Errorf("Aliases(\"\") = %v, want none", got)
	}
}

func TestMatchesAcceptsEverySpelling(t *testing.T) {
	for _, tag := range []string{"english", "eng", "en", "EN", "en-US", "en_GB", " eng "} {
		if !Matches(tag, "english") {
			t.Errorf("Matches(%q, english) = false", tag)
		}
	}
	for _, tag := range []string{"hindi", "hin", "hi", "", "e"} {
		if Matches(tag, "english") {
			t.Errorf("Matches(%q, english) = true", tag)
		}
	}
}

// Every language the table knows must round-trip through Matches under all
// three of its spellings. This is what stops a half-added entry.
func TestEverySupportedLanguageMatchesItsOwnAliases(t *testing.T) {
	for _, name := range Supported() {
		for _, tag := range Aliases(name) {
			if !Matches(tag, name) {
				t.Errorf("Matches(%q, %q) = false; the table is internally inconsistent", tag, name)
			}
		}
	}
}

// The languages a user can ask for must be the same set everywhere. Russian was
// selectable for the player but unknown to the provider.
func TestSupportedCoversTheLanguagesTheCLIAdvertises(t *testing.T) {
	for _, name := range []string{
		"english", "spanish", "french", "german", "italian", "portuguese",
		"russian", "japanese", "korean", "chinese", "arabic", "turkish",
		"hindi", "tamil", "telugu", "dutch", "polish",
	} {
		if len(Aliases(name)) < 3 {
			t.Errorf("%q has no ISO aliases; it would match only its own spelling", name)
		}
	}
}

// ISO 639-2 has bibliographic and terminological variants for several
// languages, and sources use both. Collapsing to a single code silently
// dropped fra and deu, which the provider table had supported before.
func TestMatchesBothISO6392Variants(t *testing.T) {
	for pref, tags := range map[string][]string{
		"french":  {"fre", "fra"},
		"german":  {"ger", "deu"},
		"chinese": {"chi", "zho"},
		"dutch":   {"dut", "nld"},
	} {
		for _, tag := range tags {
			if !Matches(tag, pref) {
				t.Errorf("Matches(%q, %q) = false; both ISO 639-2 variants must match", tag, pref)
			}
		}
	}
}
