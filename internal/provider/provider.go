// Package provider defines the interface for media content providers
// and their implementations.
package provider

import (
	"lobster/internal/media"
)

// Provider is the interface that content providers must implement.
type Provider interface {
	// Search returns matching results for a query.
	Search(query string) ([]media.SearchResult, error)

	// GetDetails returns detailed metadata for a content item.
	GetDetails(id string) (*media.ContentDetail, error)

	// GetSeasons returns available seasons for a TV show.
	GetSeasons(id string) ([]media.Season, error)

	// GetEpisodes returns episodes for a given season.
	GetEpisodes(id string, seasonID string) ([]media.Episode, error)

	// GetServers returns available streaming servers.
	// For movies, episodeID is empty.
	GetServers(id string, episodeID string) ([]media.Server, error)

	// GetEmbedURL returns the embed URL for a given server.
	GetEmbedURL(serverID string) (string, error)

	// Trending returns trending content.
	Trending(mediaType media.MediaType) ([]media.SearchResult, error)

	// Recent returns recently added content.
	Recent(mediaType media.MediaType) ([]media.SearchResult, error)
}
