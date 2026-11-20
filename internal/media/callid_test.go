package media

import "testing"

func TestCallIDPrefersTheProviderID(t *testing.T) {
	cases := []struct {
		name string
		r    SearchResult
		want string
	}{
		{"own row", SearchResult{ID: "tv/one-piece-100"}, "tv/one-piece-100"},
		{"resolved catalogue row", SearchResult{ID: "tv/37854", ProviderID: "tv/one-piece-100", FromCatalogue: true}, "tv/one-piece-100"},
		{"unresolved catalogue row", SearchResult{ID: "tv/37854", FromCatalogue: true, Unresolved: true}, "tv/37854"},
	}
	for _, c := range cases {
		if got := c.r.CallID(); got != c.want {
			t.Errorf("%s: CallID() = %q, want %q", c.name, got, c.want)
		}
	}
}
