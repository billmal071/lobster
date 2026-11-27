package config

import (
	"os"
	"strings"
)

const TMDBKeyEnv = "LOBSTER_TMDB_KEY"

func TMDBKeyFromEnv() string {
	return strings.TrimSpace(os.Getenv(TMDBKeyEnv))
}
