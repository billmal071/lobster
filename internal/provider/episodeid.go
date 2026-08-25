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
	showID, epNum := mediaID, 1
	if episodeID != "" {
		parts := strings.SplitN(episodeID, ":", 3)
		if len(parts) != 3 {
			return "", fmt.Errorf("bad episode id %q", episodeID)
		}
		season, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", fmt.Errorf("bad episode id %q", episodeID)
		}
		if season > 1 {
			return "", fmt.Errorf("no season %d (seasons are separate shows)", season)
		}
		epNum, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", fmt.Errorf("bad episode id %q", episodeID)
		}
		showID = parts[0]
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
