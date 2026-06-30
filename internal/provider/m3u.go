package provider

import (
	"regexp"
	"strings"
)

// Channel is one IPTV channel parsed from an M3U playlist.
type Channel struct {
	ID         string   // tvg-id if present, else slug(Name); uniqueness enforced by the provider
	Name       string   // display name (text after the EXTINF comma)
	Logo       string   // tvg-logo URL ("" if none)
	Categories []string // group-title split on ";"; ["Uncategorized"] if empty/Undefined
	URL        string   // stream URL
	Referer    string   // from #EXTVLCOPT:http-referrer
	UserAgent  string   // from #EXTVLCOPT:http-user-agent
}

var (
	m3uAttrRe = regexp.MustCompile(`([a-zA-Z0-9-]+)="([^"]*)"`)
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
)

// ParseM3U turns raw M3U bytes into channels. It is tolerant of empty
// attributes, commas inside quoted attributes and names, missing URL lines,
// unknown # directives, blank lines, and CRLF/LF endings.
func ParseM3U(data []byte) []Channel {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out []Channel
	var cur *Channel
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXTINF:"):
			cur = parseExtinf(line)
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			if cur != nil {
				applyVlcOpt(cur, strings.TrimPrefix(line, "#EXTVLCOPT:"))
			}
		case strings.HasPrefix(line, "#"):
			continue // unknown directive
		default:
			if cur == nil {
				continue
			}
			cur.URL = line
			out = append(out, *cur)
			cur = nil
		}
	}
	return out
}

func parseExtinf(line string) *Channel {
	attrs := map[string]string{}
	for _, m := range m3uAttrRe.FindAllStringSubmatch(line, -1) {
		attrs[strings.ToLower(m[1])] = m[2]
	}
	c := &Channel{
		Name:       extinfName(line),
		Logo:       attrs["tvg-logo"],
		Categories: splitCategories(attrs["group-title"]),
	}
	if id := attrs["tvg-id"]; id != "" {
		c.ID = id
	} else {
		c.ID = slug(c.Name)
	}
	return c
}

// extinfName returns the text after the comma that follows the final closing
// quote of the attribute block (so commas inside attributes don't truncate it).
func extinfName(line string) string {
	start := strings.LastIndex(line, `"`)
	comma := -1
	if start >= 0 {
		if rel := strings.Index(line[start:], ","); rel >= 0 {
			comma = start + rel
		}
	} else {
		comma = strings.Index(line, ",")
	}
	if comma < 0 {
		return ""
	}
	return strings.TrimSpace(line[comma+1:])
}

func splitCategories(g string) []string {
	g = strings.TrimSpace(g)
	if g == "" || strings.EqualFold(g, "Undefined") {
		return []string{"Uncategorized"}
	}
	var cats []string
	for _, p := range strings.Split(g, ";") {
		if p = strings.TrimSpace(p); p != "" {
			cats = append(cats, p)
		}
	}
	if len(cats) == 0 {
		return []string{"Uncategorized"}
	}
	return cats
}

func applyVlcOpt(c *Channel, opt string) {
	key, val, ok := strings.Cut(opt, "=") // first '=' only
	if !ok {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "http-user-agent":
		c.UserAgent = val
	case "http-referrer":
		c.Referer = val
	}
}

func slug(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	return strings.Trim(s, "-")
}
