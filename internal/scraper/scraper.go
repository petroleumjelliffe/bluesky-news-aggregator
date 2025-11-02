package scraper

import (
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
	client *http.Client
}

// NewScraper creates a new scraper
func NewScraper() *Scraper {
	return &Scraper{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// FetchOGData fetches OpenGraph metadata from a URL
func (s *Scraper) FetchOGData(url string) (*OGData, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Set user agent to avoid being blocked
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; BlueskyNewsBot/1.0)")

	resp, err := s.client.Do(req)
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
