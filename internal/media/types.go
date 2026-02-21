// Package media defines shared types for the lobster application.
package media

// MediaType represents whether content is a movie or TV show.
type MediaType int

const (
	Movie MediaType = iota
	TV
)

func (m MediaType) String() string {
	switch m {
	case Movie:
		return "movie"
	case TV:
		return "tv"
	default:
		return "unknown"
	}
}

// SearchResult represents a single search result from a provider.
type SearchResult struct {
	ID       string    // Provider-specific ID (e.g., "movie/free-the-exorcist-hd-75043")
	Title    string    // Display title
	Type     MediaType // Movie or TV
	Year     string    // Release year
	Seasons  int       // Number of seasons (TV only)
	Episodes int       // Total episodes (TV only)
	URL      string    // Full URL to the content page
}

// Season represents a TV show season.
type Season struct {
	Number int
	ID     string // Provider-specific season ID
}

// Episode represents a TV show episode.
type Episode struct {
	Number int
	Title  string
	ID     string // Provider-specific episode ID
}

// Server represents a streaming server option.
type Server struct {
	Name string // e.g., "Vidcloud", "UpCloud"
	ID   string // Server/data-id
}

// Stream contains the resolved streaming URLs.
type Stream struct {
	URL       string     // m3u8 or direct video URL
	Subtitles []Subtitle // Available subtitle tracks
	Quality   string     // Resolved quality
}

// Subtitle represents a subtitle track.
type Subtitle struct {
	Language string // e.g., "English"
	Label    string // Display label, e.g., "English - SDH"
	URL      string // URL to the subtitle file (usually VTT)
}

// HistoryEntry represents a single entry in the watch history.
type HistoryEntry struct {
	ID       string    // Provider content ID
	Title    string    // Display title
	Type     MediaType // Movie or TV
	Season   int       // Season number (TV only, 0 for movies)
	Episode  int       // Episode number (TV only, 0 for movies)
	Position float64   // Last playback position in seconds
	Duration float64   // Total duration in seconds
}
