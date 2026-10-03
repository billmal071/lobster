package provider

import (
	"fmt"
	"strconv"
	"strings"

	"lobster/internal/media"
)

// resolveNumericEpisodeID converts a fallback-resolver episode ID
// ("showID:season:episode", or "" meaning episode 1 of mediaID) into the
// provider's native episode ID by looking up its episode catalog. Anime
// providers have no season concept — multi-season shows are separate entries
// — so anything beyond season 1 is refused rather than risking playback of
// the wrong episode.
func resolveNumericEpisodeID(getEpisodes func(id, seasonID string) ([]media.Episode, error), mediaID, episodeID string) (string, error) {
	showID, epNum, err := parseFallbackEpisodeRef(mediaID, episodeID)
	if err != nil {
		return "", err
	}
	eps, err := getEpisodes(showID, showID)
	if err != nil {
		return "", err
	}
	for _, ep := range eps {
		if ep.Number == epNum {
			return ep.ID, nil
		}
	}
	return "", fmt.Errorf("episode %d not found for %s", epNum, showID)
}

// parseFallbackEpisodeRef splits a fallback-resolver episode ID into the show
// it names and the episode number it asks for, without consulting any
// provider. "" means episode 1 of mediaID, which is what the resolver sends
// for a film and for a request carrying no season or episode.
//
// It is split out of resolveNumericEpisodeID for the providers where the
// catalogue lookup buys nothing. AnimeOnsen numbers its episodes in the URL,
// so its native episode ID *is* the episode number and the lookup only ever
// reproduced the number it was handed — while costing a full enumeration and,
// worse, inheriting its failures: an enumeration that returns its measured
// prefix alongside ErrIncompleteEpisodeList is an answer, but it is an error
// too, and every error here is fatal. Playback then failed exactly when the
// list was short. Providers whose episode IDs are opaque (AllAnime, AniPub)
// still need the lookup and still go through resolveNumericEpisodeID.
//
// The season refusal stays here, not in the callers: on these sources a second
// season is a separate catalogue entry, so season 2 of this ID is not a thing
// that exists and must be refused rather than silently played as season 1.
func parseFallbackEpisodeRef(mediaID, episodeID string) (string, int, error) {
	if episodeID == "" {
		return mediaID, 1, nil
	}
	parts := strings.SplitN(episodeID, ":", 3)
	if len(parts) != 3 {
		return "", 0, fmt.Errorf("bad episode id %q", episodeID)
	}
	season, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("bad episode id %q", episodeID)
	}
	if season > 1 {
		return "", 0, fmt.Errorf("no season %d (seasons are separate shows)", season)
	}
	epNum, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, fmt.Errorf("bad episode id %q", episodeID)
	}
	return parts[0], epNum, nil
}
