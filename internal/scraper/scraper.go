package scraper

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// OGData holds OpenGraph metadata
type OGData struct {
	Title       string
	Description string
	ImageURL    string
}

// DomainRateLimiter enforces per-domain rate limiting
type DomainRateLimiter struct {
	lastRequest map[string]time.Time
	mu          sync.RWMutex
	minDelay    time.Duration
}

// NewDomainRateLimiter creates a new rate limiter
func NewDomainRateLimiter(minDelay time.Duration) *DomainRateLimiter {
	return &DomainRateLimiter{
		lastRequest: make(map[string]time.Time),
		minDelay:    minDelay,
	}
}

// Wait blocks until enough time has passed since last request to domain
func (d *DomainRateLimiter) Wait(domain string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if last, exists := d.lastRequest[domain]; exists {
		elapsed := time.Since(last)
		if elapsed < d.minDelay {
			time.Sleep(d.minDelay - elapsed)
		}
	}
	d.lastRequest[domain] = time.Now()
}

// Scraper fetches OpenGraph data from URLs
type Scraper struct {
	client       *http.Client
	http1Client  *http.Client
	rateLimiter  *DomainRateLimiter
	maxBodySize  int64
	maxRetries   int
}

// NewScraper creates a new scraper
func NewScraper() *Scraper {
	// Default client with HTTP/2 support
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	// HTTP/1.1-only client for fallback
	http1Transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	// Explicitly disable HTTP/2
	http1Transport.TLSNextProto = make(map[string]func(authority string, c *tls.Conn) http.RoundTripper)

	http1Client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: http1Transport,
	}

	return &Scraper{
		client:      client,
		http1Client: http1Client,
		rateLimiter: NewDomainRateLimiter(1 * time.Second), // 1 req/sec per domain
		maxBodySize: 1024 * 1024,                           // 1MB limit
		maxRetries:  2,                                     // Retry transient errors twice
	}
}

// FetchOGData fetches OpenGraph metadata from a URL with retry logic
func (s *Scraper) FetchOGData(urlStr string) (*OGData, error) {
	// Extract domain for rate limiting
	domain, err := extractDomain(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	// Rate limit per domain
	s.rateLimiter.Wait(domain)

	// Retry with exponential backoff
	backoff := 500 * time.Millisecond
	var lastErr error

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		data, err := s.fetchOnce(urlStr)
		if err == nil {
			return data, nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			return nil, err
		}

		// Don't sleep after last attempt
		if attempt < s.maxRetries {
			delay := backoff * time.Duration(1<<attempt) // Exponential: 500ms, 1s
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed after %d retries: %w", s.maxRetries, lastErr)
}

// fetchOnce attempts to fetch OG data once, with HTTP/2 fallback
func (s *Scraper) fetchOnce(urlStr string) (*OGData, error) {
	// Try with default HTTP/2 client first
	data, err := s.fetchWithClient(urlStr, s.client)
	if err != nil {
		// Check if it's an HTTP/2 stream error
		if strings.Contains(err.Error(), "stream error") || strings.Contains(err.Error(), "INTERNAL_ERROR") {
			// Retry with HTTP/1.1 client
			return s.fetchWithClient(urlStr, s.http1Client)
		}
		return nil, err
	}
	return data, nil
}

// extractDomain extracts the domain from a URL
func extractDomain(urlStr string) (string, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	return parsed.Host, nil
}

// isRetryableError determines if an error should be retried
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Retry transient errors
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "502") {
		return true
	}

	// Don't retry permanent errors
	if strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "410") ||
		strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "400") {
		return false
	}

	// Default: don't retry unknown errors
	return false
}

// fetchWithClient performs the actual HTTP request with the given client
func (s *Scraper) fetchWithClient(urlStr string, client *http.Client) (*OGData, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	// Set browser-like headers
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}

	// Limit body size to prevent reading huge files
	limitedReader := io.LimitReader(resp.Body, s.maxBodySize)

	doc, err := goquery.NewDocumentFromReader(limitedReader)
	if err != nil {
		return nil, err
	}

	data := &OGData{}

	// Extract OpenGraph tags
	doc.Find("meta").Each(func(i int, s *goquery.Selection) {
		property, _ := s.Attr("property")
		content, _ := s.Attr("content")

		switch property {
		case "og:title":
			data.Title = content
		case "og:description":
			data.Description = content
		case "og:image":
			data.ImageURL = content
		}
	})

	// Fallback to standard HTML tags if OG tags not found
	if data.Title == "" {
		data.Title = strings.TrimSpace(doc.Find("title").First().Text())
	}

	if data.Description == "" {
		desc, exists := doc.Find("meta[name='description']").Attr("content")
		if exists {
			data.Description = desc
		}
	}

	// Try Twitter card as fallback for image
	if data.ImageURL == "" {
		twitterImage, exists := doc.Find("meta[name='twitter:image']").Attr("content")
		if exists {
			data.ImageURL = twitterImage
		}
	}

	return data, nil
}
