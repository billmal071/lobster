package extract

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"lobster/internal/httputil"
	"lobster/internal/media"
)

// NetuExtractor extracts streams from strcdn.org (Netu) embed pages.
// These pages contain HLS stream URLs and subtitle links directly in the HTML.
type NetuExtractor struct {
	client *http.Client
}

// NewNetu creates a new NetuExtractor.
func NewNetu() *NetuExtractor {
	return &NetuExtractor{
		client: httputil.NewClient(),
	}
}

var (
	netuM3U8Re  = regexp.MustCompile(`https?://[^'"` + "`" + `\s]*\.m3u8[^'"` + "`" + `\s]*`)
	netuSubRe   = regexp.MustCompile(`file2sub\("([^"]+)","([^"]+)","([^"]+)"`)
)

// Extract resolves a strcdn.org or vidcdn.co embed URL into a stream.
func (n *NetuExtractor) Extract(embedURL string, preferredQuality string) (*media.Stream, error) {
	// Step 1: Resolve the actual embed URL if it's a vidcdn.co redirect
	pageURL := embedURL
	if strings.Contains(embedURL, "vidcdn.co") {
		resolved, err := followRedirect(embedURL)
		if err != nil {
			return nil, fmt.Errorf("following redirect: %w", err)
		}
		pageURL = resolved
	}

	// Step 2: The /f/ path is a full page that JS-redirects to /e/ in iframes.
	// Convert /f/ to /e/ directly to get the embed page with m3u8 URLs.
	if strings.Contains(pageURL, "/f/") {
		pageURL = strings.Replace(pageURL, "/f/", "/e/", 1)
	}

	// Fetch the embed page (follow redirects)
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/121.0")
	req.Header.Set("Referer", "https://flixhq.ws/")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching embed page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed page returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("reading page: %w", err)
	}
	html := string(body)

	// Step 3: Extract m3u8 URL
	m3u8Match := netuM3U8Re.FindString(html)
	if m3u8Match == "" {
		return nil, fmt.Errorf("no m3u8 URL found on page")
	}

	// Step 4: Extract subtitles
	var subtitles []media.Subtitle
	subMatches := netuSubRe.FindAllStringSubmatch(html, -1)
	for _, m := range subMatches {
		if len(m) >= 4 {
			url := m[1]
			if strings.HasPrefix(url, "//") {
				url = "https:" + url
			}
			subtitles = append(subtitles, media.Subtitle{
				Language: m[3],
				Label:    m[3],
				URL:      url,
			})
		}
	}

	return &media.Stream{
		URL:       m3u8Match,
		Subtitles: subtitles,
		Quality:   preferredQuality,
	}, nil
}

// followRedirect follows a single redirect and returns the Location header.
func followRedirect(url string) (string, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://flixhq.ws/")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect location")
	}
	return loc, nil
}
