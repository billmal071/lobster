package provider

import (
	"fmt"
	"net/http"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// allanimeConfig holds the volatile AllAnime constants. They rotate upstream
// every few weeks; this is the single place to update them.
type allanimeConfig struct {
	apiBase       string
	clockBase     string
	refererSite   string
	sourcesOrigin string
	userAgent     string
	pqHash        string
	passphrase    string
	xorByte       byte
}

func defaultAllanimeConfig() allanimeConfig {
	return allanimeConfig{
		apiBase:       "https://api.allanime.day/api",
		clockBase:     "https://allanime.day",
		refererSite:   "https://allanime.to",
		sourcesOrigin: "https://youtu-chan.com",
		userAgent:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:109.0) Gecko/20100101 Firefox/121.0",
		pqHash:        "d405d0edd690624b66baba3068e0edc3ac90f1597d898a1ec8db4e5c43c00fec",
		passphrase:    "Xot36i3lK3:v1",
		xorByte:       0x38,
	}
}

// httpDoer is the subset of *http.Client used here (swappable in tests).
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// AllAnime is a StreamProvider backed by the AllAnime GraphQL API.
type AllAnime struct {
	cfg    allanimeConfig
	client httpDoer
	trans  string // "sub" | "dub" | "raw"
}

// NewAllAnime constructs a provider defaulting to dub or sub.
func NewAllAnime(dub bool) *AllAnime {
	t := "sub"
	if dub {
		t = "dub"
	}
	return &AllAnime{cfg: defaultAllanimeConfig(), client: httputil.NewClient(), trans: t}
}

func (a *AllAnime) Translation() string       { return a.trans }
func (a *AllAnime) SetTranslation(t string)   { a.trans = t }

// --- Provider / StreamProvider methods (filled in by later tasks) ---

func (a *AllAnime) Search(query string) ([]media.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *AllAnime) GetSeasons(id string) ([]media.Season, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *AllAnime) GetEpisodes(id, seasonID string) ([]media.Episode, error) {
	return nil, fmt.Errorf("not implemented")
}
func (a *AllAnime) Watch(mediaID, episodeID, server, quality string) (*media.Stream, error) {
	return nil, fmt.Errorf("not implemented")
}

// Stable trivial methods.
func (a *AllAnime) GetDetails(id string) (*media.ContentDetail, error) {
	return &media.ContentDetail{}, nil
}
func (a *AllAnime) GetServers(id, episodeID string) ([]media.Server, error) {
	return []media.Server{{Name: "AllAnime", ID: "default"}}, nil
}
func (a *AllAnime) GetEmbedURL(serverID string) (string, error) {
	return "", fmt.Errorf("use Watch instead")
}
func (a *AllAnime) Trending(mt media.MediaType) ([]media.SearchResult, error) { return nil, nil }
func (a *AllAnime) Recent(mt media.MediaType) ([]media.SearchResult, error)   { return nil, nil }
