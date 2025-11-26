package scraper

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	readability "github.com/go-shiori/go-readability"
)

// ArticleContent holds extracted article data including full text
type ArticleContent struct {
	Title       string
	Description string
	ImageURL    string
	FullText    string // Main article content extracted via Readability
	Excerpt     string // Short excerpt
	Author      string
	PublishedAt string
	URL         string
	Byline      string // Author from article itself
	SiteName    string
}

// ExtractArticleContent fetches and extracts full article content using Mozilla's Readability
func (s *Scraper) ExtractArticleContent(urlStr string) (*ArticleContent, error) {
	// Extract domain for rate limiting
	domain, err := extractDomain(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Rate limit per domain
	s.rateLimiter.Wait(domain)

	// Fetch the HTML
	html, err := s.fetchHTML(urlStr)
	if err != nil {
		return nil, err
	}

	// Parse with Readability
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	article, err := readability.FromReader(strings.NewReader(html), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse article: %w", err)
	}

	publishedAt := ""
	if article.PublishedTime != nil {
		publishedAt = article.PublishedTime.Format("2006-01-02T15:04:05Z07:00")
	}

	content := &ArticleContent{
		URL:         urlStr,
		Title:       article.Title,
		Byline:      article.Byline,
		FullText:    article.TextContent, // Plain text version
		Excerpt:     article.Excerpt,
		ImageURL:    article.Image,
		SiteName:    article.SiteName,
		PublishedAt: publishedAt,
	}

	return content, nil
}

// fetchHTML fetches raw HTML from URL with retry logic
func (s *Scraper) fetchHTML(urlStr string) (string, error) {
	// Try with default HTTP/2 client first
	html, err := s.fetchHTMLWithClient(urlStr, s.client)
	if err != nil {
		// Check if it's an HTTP/2 stream error
		if strings.Contains(err.Error(), "stream error") || strings.Contains(err.Error(), "INTERNAL_ERROR") {
			// Retry with HTTP/1.1 client
			return s.fetchHTMLWithClient(urlStr, s.http1Client)
		}
		return "", err
	}
	return html, nil
}

// fetchHTMLWithClient fetches HTML with specific HTTP client
func (s *Scraper) fetchHTMLWithClient(urlStr string, client *http.Client) (string, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return "", err
	}

	// Set browser-like headers to avoid bot detection
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status code: %d", resp.StatusCode)
	}

	// Limit body size to prevent reading huge files
	limitedReader := io.LimitReader(resp.Body, s.maxBodySize)

	// Read all HTML
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", err
	}

	return string(bodyBytes), nil
}
