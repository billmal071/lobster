package extract

import (
	"fmt"
	"regexp"
	"strings"
)

// extractClientKey extracts the obfuscated client key from embed page HTML.
// The key is hidden using rotating obfuscation methods that change per request.
func extractClientKey(html string) (string, error) {
	// Regex patterns for each obfuscation method, tried in order:
	// 0: <meta name="_gg_fb" content="{KEY}">
	// 1: <!-- _is_th:{KEY} -->
	// 2: <script>window._lk_db = {x: "{P1}", y: "{P2}", z: "{P3}"};</script>
	// 3: <div data-dpi="{KEY}" ...></div>
	// 4: <script nonce="{KEY}">
	// 5: <script>window._xy_ws = "{KEY}";</script>
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`<meta name="_gg_fb" content="[a-zA-Z0-9]+">`),
		regexp.MustCompile(`<!--\s+_is_th:[0-9a-zA-Z]+\s+-->`),
		regexp.MustCompile(`<script>window\._lk_db\s+=\s+\{[xyz]:\s+["'][a-zA-Z0-9]+["'],\s+[xyz]:\s+["'][a-zA-Z0-9]+["'],\s+[xyz]:\s+["'][a-zA-Z0-9]+["']\};</script>`),
		regexp.MustCompile(`<div\s+data-dpi="[0-9a-zA-Z]+"\s+[^>]*></div>`),
		regexp.MustCompile(`<script nonce="[0-9a-zA-Z]+">`),
		regexp.MustCompile(`<script>window\._xy_ws = ['"\x60][0-9a-zA-Z]+['"\x60];</script>`),
	}

	// General key regex (extracts quoted value)
	keyRe := regexp.MustCompile(`"[a-zA-Z0-9]+"`)

	// lk_db key part regexes
	lkDbParts := []*regexp.Regexp{
		regexp.MustCompile(`x:\s+"[a-zA-Z0-9]+"`),
		regexp.MustCompile(`y:\s+"[a-zA-Z0-9]+"`),
		regexp.MustCompile(`z:\s+"[a-zA-Z0-9]+"`),
	}

	// Find the first matching pattern
	var match string
	matchIdx := -1
	for i, pat := range patterns {
		m := pat.FindString(html)
		if m != "" {
			match = m
			matchIdx = i
			break
		}
	}

	if matchIdx == -1 {
		return "", fmt.Errorf("failed to extract client key: no obfuscation pattern matched")
	}

	switch matchIdx {
	case 1:
		// Comment pattern: <!-- _is_th:{KEY} -->
		// No quotes around key, use different extraction
		re := regexp.MustCompile(`:[a-zA-Z0-9]+ `)
		keyMatch := re.FindString(match)
		if keyMatch == "" {
			return "", fmt.Errorf("failed to extract client key from comment pattern")
		}
		return strings.TrimSpace(strings.TrimPrefix(keyMatch, ":")), nil

	case 2:
		// 3-part key: window._lk_db = {x: "P1", y: "P2", z: "P3"}
		var parts []string
		for _, partRe := range lkDbParts {
			partMatch := partRe.FindString(match)
			if partMatch == "" {
				return "", fmt.Errorf("failed to build client key from lk_db pattern")
			}
			val := keyRe.FindString(partMatch)
			if val == "" {
				return "", fmt.Errorf("failed to extract value from lk_db part")
			}
			parts = append(parts, strings.Trim(val, `"`))
		}
		return strings.Join(parts, ""), nil

	default:
		// All other patterns: extract quoted value
		val := keyRe.FindString(match)
		if val == "" {
			return "", fmt.Errorf("failed to extract client key value")
		}
		return strings.Trim(val, `"`), nil
	}
}
