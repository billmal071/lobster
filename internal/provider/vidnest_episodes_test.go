package provider

import "testing"

// VidNest measures its season list by probing each season for streams
// (GetSeasons), but its episode list used to be a locally generated 1..50 —
// so every show it served claimed 50 episodes per season. That is not an
// answer, and callers cannot distinguish it from a measured one: `play
// --episode 47` passed validation and then failed at Watch, blamed on the
// provider chain. An unanswerable question must return an error, as
// TBCPLEmbed.GetEpisodes already does.
func TestVidNestGetEpisodesRefusesToInvent(t *testing.T) {
	v := NewVidNest()

	episodes, err := v.GetEpisodes("tv/1403", "1403:1")
	if err == nil {
		t.Fatalf("GetEpisodes returned %d episodes and a nil error; VidNest does not enumerate episodes and must say so", len(episodes))
	}
	if len(episodes) != 0 {
		t.Errorf("GetEpisodes returned %d episodes alongside an error; want none", len(episodes))
	}
}
