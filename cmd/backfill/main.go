package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/didmanager"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/processor"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/urlutil"
)

// Backfiller handles backfilling historical posts for followed accounts
type Backfiller struct {
	db         *database.DB
	bskyClient *bluesky.Client
	processor  *processor.Processor
	config     *config.Config
}

func main() {
	// Load configuration (supports env vars)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database (log safe connection string without password)
	log.Printf("[INFO] Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := database.NewDB(cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize Bluesky client (for API-based backfill)
	bskyClient, err := bluesky.NewClient(cfg.Bluesky.Handle, cfg.Bluesky.Password)
	if err != nil {
		log.Fatalf("Failed to create Bluesky client: %v", err)
	}

	// Create DID manager and load network accounts
	didManager := didmanager.NewManagerWithConfig(db, &didmanager.Config{
		Include2ndDegree: true,
		MinSourceCount:   2,
	})
	if err := didManager.LoadFromDatabase(); err != nil {
		log.Fatalf("Failed to load DID manager: %v", err)
	}

	// Create backfiller
	backfiller := &Backfiller{
		db:         db,
		bskyClient: bskyClient,
		processor:  processor.NewProcessor(db, didManager),
		config:     cfg,
	}

	log.Printf("[INFO] Starting backfill for accounts without completed backfill...")

	// Get all follows that need backfilling
	follows, err := db.GetAllFollows()
	if err != nil {
		log.Fatalf("Failed to get follows: %v", err)
	}

	// Filter to only those needing backfill
	needsBackfill := []database.Follow{}
	for _, follow := range follows {
		if !follow.BackfillCompleted {
			needsBackfill = append(needsBackfill, follow)
		}
	}

	log.Printf("[INFO] Found %d accounts needing backfill (out of %d total)", len(needsBackfill), len(follows))

	if len(needsBackfill) == 0 {
		log.Printf("[INFO] No accounts need backfilling. Exiting.")
		return
	}

	// Backfill concurrently
	backfiller.backfillAccounts(needsBackfill)

	log.Printf("[INFO] Backfill complete!")
}

// backfillAccounts backfills multiple accounts concurrently
func (b *Backfiller) backfillAccounts(follows []database.Follow) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, b.config.Polling.MaxConcurrent)

	successCount := 0
	failureCount := 0
	var mu sync.Mutex

	for _, follow := range follows {
		wg.Add(1)

		go func(f database.Follow) {
			defer wg.Done()

			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			err := b.backfillAccount(f)

			mu.Lock()
			if err != nil {
				log.Printf("[ERROR] %s: Backfill failed: %v", f.Handle, err)
				failureCount++
			} else {
				successCount++
			}
			mu.Unlock()

			// Rate limiting
			time.Sleep(time.Duration(b.config.Polling.RateLimitMs) * time.Millisecond)
		}(follow)
	}

	wg.Wait()

	log.Printf("[INFO] Backfill results: %d succeeded, %d failed", successCount, failureCount)
}

// backfillAccount backfills posts for a single account
func (b *Backfiller) backfillAccount(follow database.Follow) error {
	lookbackPeriod := time.Duration(b.config.Polling.InitialLookbackHours) * time.Hour
	cutoffTime := time.Now().Add(-lookbackPeriod)

	log.Printf("[BACKFILL] %s: Fetching last %d hours of posts", follow.Handle, b.config.Polling.InitialLookbackHours)

	cursor := ""
	totalPosts := 0
	totalURLs := 0
	pageCount := 0

	for pageCount < b.config.Polling.MaxPagesPerUser {
		pageCount++

		// Fetch with retry logic
		feed, err := b.fetchWithRetry(follow.Handle, cursor, 50)
		if err != nil {
			log.Printf("[BACKFILL] %s: Failed after retries on page %d: %v", follow.Handle, pageCount, err)
			return err
		}

		if len(feed.Feed) == 0 {
			log.Printf("[BACKFILL] %s: No more posts (reached end)", follow.Handle)
			break
		}

		// Process posts
		urlsInBatch := 0
		for _, item := range feed.Feed {
			urlsInBatch += b.processPost(&item.Post, follow.DID)
		}
		totalPosts += len(feed.Feed)
		totalURLs += urlsInBatch

		// Check oldest post
		oldestPost := feed.Feed[len(feed.Feed)-1]
		if oldestPost.Post.Record.CreatedAt.Before(cutoffTime) {
			log.Printf("[BACKFILL] %s: Reached %d hour cutoff at page %d", follow.Handle, b.config.Polling.InitialLookbackHours, pageCount)
			break
		}

		if feed.Cursor == "" {
			break
		}

		cursor = feed.Cursor

		// Rate limiting between pages
		time.Sleep(time.Duration(b.config.Polling.RateLimitMs) * time.Millisecond)
	}

	// Mark backfill as completed
	if err := b.db.MarkBackfillCompleted(follow.DID); err != nil {
		return fmt.Errorf("failed to mark backfill complete: %w", err)
	}

	log.Printf("[BACKFILL] %s: Complete - %d posts, %d URLs (%d pages)", follow.Handle, totalPosts, totalURLs, pageCount)
	return nil
}

// fetchWithRetry fetches a feed with exponential backoff retry logic
func (b *Backfiller) fetchWithRetry(handle, cursor string, limit int) (*bluesky.FeedResponse, error) {
	var feed *bluesky.FeedResponse
	var err error

	backoff := time.Duration(b.config.Polling.RetryBackoffMs) * time.Millisecond

	for attempt := 0; attempt <= b.config.Polling.MaxRetries; attempt++ {
		feed, err = b.bskyClient.GetAuthorFeed(handle, cursor, limit)

		if err == nil {
			return feed, nil
		}

		if attempt < b.config.Polling.MaxRetries {
			delay := backoff * time.Duration(1<<attempt) // Exponential: 1s, 2s, 4s
			log.Printf("[RETRY] %s: Attempt %d failed, retrying in %v: %v", handle, attempt+1, delay, err)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed after %d retries: %w", b.config.Polling.MaxRetries, err)
}

// processPost processes a single post from the API and stores it
func (b *Backfiller) processPost(post *bluesky.Post, did string) int {
	// Store post in database
	dbPost := &database.Post{
		ID:           post.URI,
		AuthorHandle: did, // Use DID for consistency with firehose
		Content:      post.Record.Text,
		CreatedAt:    post.Record.CreatedAt,
	}

	if err := b.db.InsertPost(dbPost); err != nil {
		log.Printf("[WARN] Error inserting post %s: %v", post.URI, err)
		return 0
	}

	urlCount := 0

	// Extract URLs from post text
	urls := extractURLsFromText(post.Record.Text)
	urlCount += b.processURLs(post.URI, urls)

	// Extract URLs from embeds
	if post.Embed != nil {
		urlCount += b.processEmbed(post.URI, post.Embed)
	}

	return urlCount
}

// processURLs processes a list of URLs and links them to a post
func (b *Backfiller) processURLs(postURI string, urls []string) int {
	urlCount := 0

	for _, rawURL := range urls {
		// Get or create link
		normalizedURL := normalizeURL(rawURL)
		link, err := b.db.GetOrCreateLink(rawURL, normalizedURL)
		if err != nil {
			log.Printf("[WARN] Error with link %s: %v", rawURL, err)
			continue
		}

		// Link post to link
		if err := b.db.LinkPostToLink(postURI, link.ID); err != nil {
			log.Printf("[WARN] Error linking post to link: %v", err)
			continue
		}

		urlCount++
	}

	return urlCount
}

// processExternalWithMetadata processes an external link with pre-fetched metadata from Bluesky
func (b *Backfiller) processExternalWithMetadata(postURI, rawURL, title, description, imageURL string) int {
	// Normalize URL
	normalizedURL := normalizeURL(rawURL)

	// Get or create link
	link, err := b.db.GetOrCreateLink(rawURL, normalizedURL)
	if err != nil {
		log.Printf("[WARN] Error with link %s: %v", rawURL, err)
		return 0
	}

	// Link post to link
	if err := b.db.LinkPostToLink(postURI, link.ID); err != nil {
		log.Printf("[WARN] Error linking post to link: %v", err)
		return 0
	}

	// Store Bluesky's metadata if we don't have any yet
	if link.Title == nil {
		if err := b.db.UpdateLinkMetadata(link.ID, title, description, imageURL); err != nil {
			log.Printf("[WARN] Error updating link metadata: %v", err)
		}
	}

	return 1
}

// processEmbed extracts URLs and metadata from embeds
func (b *Backfiller) processEmbed(postURI string, embed *bluesky.Embed) int {
	urlCount := 0

	// Handle external link embeds with metadata
	if embed.External != nil {
		// Use Bluesky's pre-fetched metadata if available
		if embed.External.Title != "" {
			urlCount += b.processExternalWithMetadata(
				postURI,
				embed.External.URI,
				embed.External.Title,
				embed.External.Description,
				embed.External.Thumb,
			)
		} else {
			// Fallback: just store URL without metadata
			urls := []string{embed.External.URI}
			urlCount += b.processURLs(postURI, urls)
		}
	}

	// Handle quote posts
	if embed.Record != nil && embed.Record.Record != nil {
		quotedPost := embed.Record.Record

		// Extract URLs from quoted post text
		urls := extractURLsFromText(quotedPost.Record.Text)
		urlCount += b.processURLs(postURI, urls)

		// Recursively process embeds in the quoted post
		if quotedPost.Embed != nil {
			urlCount += b.processEmbed(postURI, quotedPost.Embed)
		}
	}

	return urlCount
}

// extractURLsFromText extracts URLs from post text
func extractURLsFromText(text string) []string {
	return urlutil.ExtractURLs(text)
}

// normalizeURL normalizes a URL for deduplication
func normalizeURL(url string) string {
	normalized, err := urlutil.Normalize(url)
	if err != nil {
		return url // Return original if normalization fails
	}
	return normalized
}
