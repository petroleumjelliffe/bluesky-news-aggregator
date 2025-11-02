package urlutil

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/goware/urlx"
)

var (
	// Common tracking parameters to remove
	trackingParams = []string{
		"utm_source", "utm_medium", "utm_campaign",
		"utm_term", "utm_content",
		"fbclid", "gclid", "mc_cid", "mc_eid",
		"ref", "ref_src", "ref_url",
	}

	// URL pattern to extract URLs from text
	urlPattern = regexp.MustCompile(`https?://[^\s<>'"]+`)
)

// ExtractURLs finds all URLs in a text string
func ExtractURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)

	// Clean up URLs (remove trailing punctuation, etc.)
	var urls []string
	for _, match := range matches {
		cleaned := strings.TrimRight(match, ".,;:!?)")
		urls = append(urls, cleaned)
	}

	return urls
}

// Normalize normalizes a URL by:
// - Converting to lowercase (scheme and host)
// - Removing default ports
// - Sorting query parameters
// - Removing tracking parameters
// - Removing trailing slashes
// - Removing fragments
func Normalize(rawURL string) (string, error) {
	// Parse and normalize using urlx
	parsed, err := urlx.Parse(rawURL)
	if err != nil {
		return "", err
	}

	normalized, err := urlx.Normalize(parsed)
	if err != nil {
		return "", err
	}

	// Additional normalization
	u, err := url.Parse(normalized)
	if err != nil {
		return normalized, nil
	}

	// Remove tracking parameters
	q := u.Query()
	for _, param := range trackingParams {
		q.Del(param)
	}

	u.RawQuery = q.Encode()

	// Remove trailing slash for consistency (but keep for root paths)
	if u.Path != "/" {
		u.Path = strings.TrimSuffix(u.Path, "/")
	}

	// Remove fragment
	u.Fragment = ""

	return u.String(), nil
}
