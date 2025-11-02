package scraper

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// OGData holds OpenGraph metadata
type OGData struct {
	Title       string
	Description string
	ImageURL    string
}

// Scraper fetches OpenGraph data from URLs
type Scraper struct {
	client       *http.Client
	http1Client  *http.Client
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
	}
}

// FetchOGData fetches OpenGraph metadata from a URL
func (s *Scraper) FetchOGData(url string) (*OGData, error) {
	// Try with default HTTP/2 client first
	data, err := s.fetchWithClient(url, s.client)
	if err != nil {
		// Check if it's an HTTP/2 stream error
		if strings.Contains(err.Error(), "stream error") || strings.Contains(err.Error(), "INTERNAL_ERROR") {
			// Retry with HTTP/1.1 client
			return s.fetchWithClient(url, s.http1Client)
		}
		return nil, err
	}
	return data, nil
}

// fetchWithClient performs the actual HTTP request with the given client
func (s *Scraper) fetchWithClient(url string, client *http.Client) (*OGData, error) {
	req, err := http.NewRequest("GET", url, nil)
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

	doc, err := goquery.NewDocumentFromReader(resp.Body)
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
