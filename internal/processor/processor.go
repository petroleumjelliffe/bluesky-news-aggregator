package processor

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/scraper"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/urlutil"
)

// Processor handles processing of Jetstream events into the database
type Processor struct {
	db      *database.DB
	scraper *scraper.Scraper
}

// PostRecord represents the post record from Jetstream (app.bsky.feed.post)
type PostRecord struct {
	Type      string    `json:"$type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	Embed     *Embed    `json:"embed,omitempty"`
}

// Embed represents embedded content in a post
type Embed struct {
	Type     string          `json:"$type"`
	External *EmbedExternal  `json:"external,omitempty"`
	Record   *EmbedRecord    `json:"record,omitempty"`
}

// EmbedExternal represents an external link with metadata
type EmbedExternal struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Thumb       interface{} `json:"thumb,omitempty"` // Can be string URL or blob object
}

// EmbedRecord represents a quoted post (we extract URLs from it recursively)
type EmbedRecord struct {
	Record *PostRecord `json:"record,omitempty"`
}

// NewProcessor creates a new event processor
func NewProcessor(db *database.DB) *Processor {
	return &Processor{
		db:      db,
		scraper: scraper.NewScraper(),
	}
}

// ProcessEvent processes a Jetstream event
func (p *Processor) ProcessEvent(event *models.Event) error {
	// Only process commit events for posts
	if event.Kind != "commit" || event.Commit == nil {
		return nil
	}

	if event.Commit.Operation != "create" || event.Commit.Collection != "app.bsky.feed.post" {
		return nil
	}

	// Decode the post record
	var postRecord PostRecord
	if err := json.Unmarshal(event.Commit.Record, &postRecord); err != nil {
		return fmt.Errorf("failed to decode post record: %w", err)
	}

	// Build post URI (at://{did}/{collection}/{rkey})
	postURI := fmt.Sprintf("at://%s/%s/%s", event.Did, event.Commit.Collection, event.Commit.RKey)

	// Store post in database (we need to resolve DID to handle)
	// For now we'll use DID as handle since we're tracking by DID
	dbPost := &database.Post{
		ID:           postURI,
		AuthorHandle: event.Did, // We'll store DID here since we have it
		Content:      postRecord.Text,
		CreatedAt:    postRecord.CreatedAt,
	}

	if err := p.db.InsertPost(dbPost); err != nil {
		return fmt.Errorf("failed to insert post: %w", err)
	}

	// Process URLs
	urlCount := 0

	// Extract URLs from post text
	urls := urlutil.ExtractURLs(postRecord.Text)
	urlCount += p.processURLs(postURI, urls)

	// Process embeds (quote posts, external links)
	if postRecord.Embed != nil {
		// Debug: Log embed data to see what Jetstream is sending
		if embedJSON, err := json.Marshal(postRecord.Embed); err == nil {
			log.Printf("[DEBUG-EMBED] %s: %s", event.Did, string(embedJSON))
		}
		urlCount += p.processEmbed(postURI, event.Did, postRecord.Embed)
	}

	if urlCount > 0 {
		log.Printf("[POST] %s: %d URLs extracted", event.Did, urlCount)
	}

	return nil
}

// processURLs processes a list of URLs and links them to a post
func (p *Processor) processURLs(postURI string, urls []string) int {
	urlCount := 0

	for _, rawURL := range urls {
		// Normalize URL
		normalizedURL, err := urlutil.Normalize(rawURL)
		if err != nil {
			log.Printf("[WARN] Error normalizing URL %s: %v", rawURL, err)
			continue
		}

		// Get or create link
		link, err := p.db.GetOrCreateLink(rawURL, normalizedURL)
		if err != nil {
			log.Printf("[WARN] Error with link %s: %v", rawURL, err)
			continue
		}

		// Link post to link
		if err := p.db.LinkPostToLink(postURI, link.ID); err != nil {
			log.Printf("[WARN] Error linking post to link: %v", err)
			continue
		}

		urlCount++

		// Fetch OG data synchronously if not already fetched
		if link.Title == nil {
			ogData, err := p.scraper.FetchOGData(normalizedURL)
			if err != nil {
				log.Printf("[WARN] Failed to fetch metadata for %s: %v", normalizedURL, err)
				// Mark as fetched to avoid retry storms
				if err := p.db.MarkLinkFetched(link.ID); err != nil {
					log.Printf("[WARN] Failed to mark link as fetched: %v", err)
				}
			} else if ogData.Title != "" || ogData.Description != "" || ogData.ImageURL != "" {
				// Update with fetched metadata
				if err := p.db.UpdateLinkMetadata(link.ID, ogData.Title, ogData.Description, ogData.ImageURL); err != nil {
					log.Printf("[WARN] Failed to update link metadata: %v", err)
				}
			} else {
				// No metadata found, mark as fetched
				if err := p.db.MarkLinkFetched(link.ID); err != nil {
					log.Printf("[WARN] Failed to mark link as fetched: %v", err)
				}
			}
		}
	}

	return urlCount
}

// processEmbed extracts URLs from embeds (quote posts, external links, etc.)
func (p *Processor) processEmbed(postURI string, authorDID string, embed *Embed) int {
	urlCount := 0

	// Handle external link embeds
	if embed.External != nil {
		// Extract thumb URL (can be string or blob object)
		thumbURL := ""
		if thumb, ok := embed.External.Thumb.(string); ok {
			thumbURL = thumb
		} else if thumbMap, ok := embed.External.Thumb.(map[string]interface{}); ok {
			// Handle blob reference: extract CID and construct CDN URL
			if ref, hasRef := thumbMap["ref"].(map[string]interface{}); hasRef {
				if cid, hasCID := ref["$link"].(string); hasCID {
					// Construct Bluesky CDN URL
					thumbURL = fmt.Sprintf("https://cdn.bsky.app/img/feed_thumbnail/plain/%s/%s@jpeg", authorDID, cid)
				}
			}
		}

		// Use Bluesky's pre-fetched metadata if available
		if embed.External.Title != "" {
			urlCount += p.processExternalWithMetadata(
				postURI,
				embed.External.URI,
				embed.External.Title,
				embed.External.Description,
				thumbURL,
			)
		} else {
			// Fallback: scrape if Bluesky didn't fetch metadata
			urls := []string{embed.External.URI}
			urlCount += p.processURLs(postURI, urls)
		}
	}

	// Handle quote posts (embedded records)
	if embed.Record != nil && embed.Record.Record != nil {
		quotedPost := embed.Record.Record

		// Extract URLs from quoted post text
		urls := urlutil.ExtractURLs(quotedPost.Text)
		urlCount += p.processURLs(postURI, urls)

		// Recursively process embeds in the quoted post
		// Note: quoted posts still use the same author DID for blob references
		if quotedPost.Embed != nil {
			urlCount += p.processEmbed(postURI, authorDID, quotedPost.Embed)
		}
	}

	return urlCount
}

// processExternalWithMetadata processes an external link with pre-fetched metadata from Bluesky
func (p *Processor) processExternalWithMetadata(postURI, rawURL, title, description, imageURL string) int {
	// Normalize URL
	normalizedURL, err := urlutil.Normalize(rawURL)
	if err != nil {
		log.Printf("[WARN] Error normalizing URL %s: %v", rawURL, err)
		return 0
	}

	// Get or create link
	link, err := p.db.GetOrCreateLink(rawURL, normalizedURL)
	if err != nil {
		log.Printf("[WARN] Error with link %s: %v", rawURL, err)
		return 0
	}

	// Link post to link
	if err := p.db.LinkPostToLink(postURI, link.ID); err != nil {
		log.Printf("[WARN] Error linking post to link: %v", err)
		return 0
	}

	// Store Bluesky's metadata if we don't have any yet
	if link.Title == nil {
		if err := p.db.UpdateLinkMetadata(link.ID, title, description, imageURL); err != nil {
			log.Printf("[WARN] Error updating link metadata: %v", err)
		}
	}

	return 1
}
