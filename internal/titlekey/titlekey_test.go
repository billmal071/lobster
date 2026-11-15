package titlekey

import "testing"

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Shōgun":                  "shogun",
		"Pokémon":                 "pokemon",
		"Law & Order":             "law and order",
		"Rick&Morty":              "rick and morty",
		"Marvel's Agents":         "marvels agents",
		"Marvel’s Agents":         "marvels agents",
		"Spider-Man: No Way Home": "spider man no way home",
		"S.H.I.E.L.D.":            "s h i e l d",
		"":                        "",
		"  ...  ":                 "",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Normalize("sho gun") == Normalize("shogun") {
		t.Error("a split word keyed the same as the whole word")
	}
	if Normalize("Law Order") == Normalize("Law & Order") {
		t.Error("& was dropped instead of read as and")
	}
}
