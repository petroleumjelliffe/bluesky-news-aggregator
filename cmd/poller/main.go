package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/scraper"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/urlutil"
)

// Config holds application configuration
type Config struct {
	DatabaseURL           string
	BlueskyHandle         string
	BlueskyPassword       string
	PollingInterval       time.Duration
	PostsPerPage          int
	MaxConcurrent         int
	RateLimitMS           int
	InitialLookbackHours  int
	MaxRetries            int
	RetryBackoffMS        int
	MaxPagesPerUser       int
}

// Poller handles the polling of Bluesky feeds
type Poller struct {
	db         *database.DB
	bskyClient *bluesky.Client
	scraper    *scraper.Scraper
	userHandle string
	config     *Config
}

func main() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database
	db, err := database.NewDB(config.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Initialize Bluesky client
	bskyClient, err := bluesky.NewClient(config.BlueskyHandle, config.BlueskyPassword)
	if err != nil {
		log.Fatalf("Failed to create Bluesky client: %v", err)
	}

	// Create poller
	poller := &Poller{
		db:         db,
		bskyClient: bskyClient,
		scraper:    scraper.NewScraper(),
		userHandle: config.BlueskyHandle,
		config:     config,
	}

	log.Printf("Starting poller for %s", config.BlueskyHandle)

	// Run initial poll
	poller.Poll()

	// Run on schedule
	ticker := time.NewTicker(config.PollingInterval)
	defer ticker.Stop()

	for range ticker.C {
		poller.Poll()
	}
}

func loadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	log.Printf("Using config file: %s", viper.ConfigFileUsed())

	// Build connection string, handling empty password
	password := viper.GetString("database.password")
	var dbURL string
	if password == "" {
		dbURL = fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	} else {
		dbURL = fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			password,
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	}

	log.Printf("Database URL: %s", dbURL)

	return &Config{
		DatabaseURL:          dbURL,
		BlueskyHandle:        viper.GetString("bluesky.handle"),
		BlueskyPassword:      viper.GetString("bluesky.password"),
		PollingInterval:      time.Duration(viper.GetInt("polling.interval_minutes")) * time.Minute,
		PostsPerPage:         viper.GetInt("polling.posts_per_page"),
		MaxConcurrent:        viper.GetInt("polling.max_concurrent"),
		RateLimitMS:          viper.GetInt("polling.rate_limit_ms"),
		InitialLookbackHours: viper.GetInt("polling.initial_lookback_hours"),
		MaxRetries:           viper.GetInt("polling.max_retries"),
		RetryBackoffMS:       viper.GetInt("polling.retry_backoff_ms"),
		MaxPagesPerUser:      viper.GetInt("polling.max_pages_per_user"),
	}, nil
}

// Poll fetches new posts from all followed accounts
func (p *Poller) Poll() {
	log.Println("Starting poll...")
	startTime := time.Now()

	// Get follows list
	follows, err := p.bskyClient.GetFollows(p.userHandle)
	if err != nil {
		log.Printf("Error getting follows: %v", err)
		return
	}

	log.Printf("Polling %d accounts", len(follows))

	// Poll each account concurrently
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, p.config.MaxConcurrent)

	for _, handle := range follows {
		wg.Add(1)

		go func(h string) {
			defer wg.Done()

			semaphore <- struct{}{}        // Acquire
			defer func() { <-semaphore }() // Release

			p.pollAccount(h)

			// Rate limiting
			time.Sleep(time.Duration(p.config.RateLimitMS) * time.Millisecond)
		}(handle)
	}

	wg.Wait()

	duration := time.Since(startTime)
	log.Printf("Poll complete in %v", duration)
}

// pollAccount fetches posts from a single account
func (p *Poller) pollAccount(handle string) {
	// Check if initial ingestion needed
	cursor, err := p.db.GetLastCursor(handle)
	if err != nil {
		log.Printf("[ERROR] %s: Failed to get cursor: %v", handle, err)
		return
	}

	if cursor == "" {
		// Initial ingestion
		if err := p.pollAccountInitial(handle); err != nil {
			if isPermanentError(err) {
				log.Printf("[SKIP] %s: Account unavailable (invalid/deleted/private): %v", handle, err)
			} else {
				log.Printf("[ERROR] %s: Initial ingestion failed: %v", handle, err)
			}
		}
	} else {
		// Regular polling with gap detection
		if err := p.pollAccountRegular(handle, cursor); err != nil {
			if isPermanentError(err) {
				log.Printf("[SKIP] %s: Account unavailable (invalid/deleted/private): %v", handle, err)
			} else {
				log.Printf("[ERROR] %s: Regular poll failed: %v", handle, err)
			}
		}
	}
}

// pollAccountInitial performs initial 24-hour ingestion for a user
func (p *Poller) pollAccountInitial(handle string) error {
	lookbackPeriod := time.Duration(p.config.InitialLookbackHours) * time.Hour
	cutoffTime := time.Now().Add(-lookbackPeriod)

	log.Printf("[INITIAL] %s: Fetching last %d hours of posts", handle, p.config.InitialLookbackHours)

	cursor := ""
	totalPosts := 0
	totalURLs := 0
	pageCount := 0

	for pageCount < p.config.MaxPagesPerUser {
		pageCount++

		// Fetch with retry logic
		feed, err := p.fetchWithRetry(handle, cursor, p.config.PostsPerPage)
		if err != nil {
			log.Printf("[INITIAL] %s: Failed after retries on page %d: %v", handle, pageCount, err)
			return err
		}

		if len(feed.Feed) == 0 {
			log.Printf("[INITIAL] %s: No more posts (reached end)", handle)
			break
		}

		// Process posts
		urlsInBatch := 0
		for _, item := range feed.Feed {
			urlsInBatch += p.processPost(&item.Post)
		}
		totalPosts += len(feed.Feed)
		totalURLs += urlsInBatch

		// Update cursor before checking if we should stop
		// This ensures we save the current position even if we break
		if feed.Cursor != "" {
			cursor = feed.Cursor
		}

		// Check oldest post
		oldestPost := feed.Feed[len(feed.Feed)-1]
		if oldestPost.Post.Record.CreatedAt.Before(cutoffTime) {
			log.Printf("[INITIAL] %s: Reached %d hour cutoff at page %d", handle, p.config.InitialLookbackHours, pageCount)
			break
		}

		if feed.Cursor == "" {
			break
		}

		// Rate limiting
		time.Sleep(time.Duration(p.config.RateLimitMS) * time.Millisecond)
	}

	// Save cursor for future polls
	if err := p.db.UpdateCursor(handle, cursor); err != nil {
		return err
	}

	log.Printf("[INITIAL] %s: Complete - %d posts, %d URLs (%d pages)", handle, totalPosts, totalURLs, pageCount)
	return nil
}

// pollAccountRegular performs regular polling with gap detection
func (p *Poller) pollAccountRegular(handle string, lastCursor string) error {
	pollingWindow := p.config.PollingInterval
	cutoffTime := time.Now().Add(-pollingWindow)

	cursor := lastCursor
	totalPosts := 0
	totalURLs := 0
	pageCount := 0

	for pageCount < 10 { // Reasonable limit for regular polling
		pageCount++

		feed, err := p.fetchWithRetry(handle, cursor, p.config.PostsPerPage)
		if err != nil {
			log.Printf("[POLL] %s: Error on page %d: %v", handle, pageCount, err)
			return err
		}

		if len(feed.Feed) == 0 {
			break
		}

		urlsInBatch := 0
		for _, item := range feed.Feed {
			urlsInBatch += p.processPost(&item.Post)
		}
		totalPosts += len(feed.Feed)
		totalURLs += urlsInBatch

		// Gap detection
		oldestPost := feed.Feed[len(feed.Feed)-1]
		if oldestPost.Post.Record.CreatedAt.Before(cutoffTime) {
			// Covered the polling window
			break
		}

		if feed.Cursor == "" {
			break
		}

		// Gap detected - log and continue
		if pageCount == 1 {
			log.Printf("[POLL] %s: High volume detected, fetching more pages", handle)
		}

		cursor = feed.Cursor
		time.Sleep(time.Duration(p.config.RateLimitMS) * time.Millisecond)
	}

	if pageCount > 1 {
		log.Printf("[POLL] %s: %d posts, %d URLs across %d pages", handle, totalPosts, totalURLs, pageCount)
	}

	// Update cursor
	return p.db.UpdateCursor(handle, cursor)
}

// fetchWithRetry fetches a feed with exponential backoff retry logic
func (p *Poller) fetchWithRetry(handle, cursor string, limit int) (*bluesky.FeedResponse, error) {
	var feed *bluesky.FeedResponse
	var err error

	backoff := time.Duration(p.config.RetryBackoffMS) * time.Millisecond

	for attempt := 0; attempt <= p.config.MaxRetries; attempt++ {
		feed, err = p.bskyClient.GetAuthorFeed(handle, cursor, limit)

		if err == nil {
			return feed, nil
		}

		// Don't retry permanent errors (400, 401, 403, 404, 410)
		if isPermanentError(err) {
			return nil, err
		}

		if attempt < p.config.MaxRetries {
			delay := backoff * time.Duration(1<<attempt) // Exponential: 1s, 2s, 4s
			log.Printf("[RETRY] %s: Attempt %d failed, retrying in %v: %v", handle, attempt+1, delay, err)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("failed after %d retries: %w", p.config.MaxRetries, err)
}

// isPermanentError checks if an API error is permanent and shouldn't be retried
func isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	// Check for HTTP status codes that indicate permanent failures
	return strings.Contains(errStr, "API error: 400") || // Bad Request (invalid handle)
		strings.Contains(errStr, "API error: 401") ||    // Unauthorized
		strings.Contains(errStr, "API error: 403") ||    // Forbidden
		strings.Contains(errStr, "API error: 404") ||    // Not Found
		strings.Contains(errStr, "API error: 410")       // Gone
}

// processPost extracts URLs and stores the post, returns number of URLs found
func (p *Poller) processPost(post *bluesky.Post) int {
	// Insert post
	dbPost := &database.Post{
		ID:           post.URI,
		AuthorHandle: post.Author.Handle,
		Content:      post.Record.Text,
		CreatedAt:    post.Record.CreatedAt,
	}

	if err := p.db.InsertPost(dbPost); err != nil {
		log.Printf("Error inserting post %s: %v", post.URI, err)
		return 0
	}

	urlCount := 0

	// Extract URLs from post text
	urls := urlutil.ExtractURLs(post.Record.Text)
	urlCount += p.processURLs(post.URI, urls)

	// Extract URLs from embeds (quote posts, external links)
	if post.Embed != nil {
		urlCount += p.processEmbed(post.URI, post.Embed)
	}

	return urlCount
}

// processURLs processes a list of URLs and links them to a post
func (p *Poller) processURLs(postURI string, urls []string) int {
	urlCount := 0

	for _, rawURL := range urls {
		// Normalize URL
		normalizedURL, err := urlutil.Normalize(rawURL)
		if err != nil {
			log.Printf("Error normalizing URL %s: %v", rawURL, err)
			continue
		}

		// Get or create link
		link, err := p.db.GetOrCreateLink(rawURL, normalizedURL)
		if err != nil {
			log.Printf("Error with link %s: %v", rawURL, err)
			continue
		}

		// Link post to link
		if err := p.db.LinkPostToLink(postURI, link.ID); err != nil {
			log.Printf("Error linking post to link: %v", err)
			continue
		}

		urlCount++

		// Fetch OG data if not already fetched
		if link.Title == nil {
			go p.fetchOGDataAsync(link.ID, normalizedURL)
		}
	}

	return urlCount
}

// processEmbed extracts URLs from embeds (quote posts, external links, etc.)
func (p *Poller) processEmbed(postURI string, embed *bluesky.Embed) int {
	urlCount := 0

	// Handle external link embeds
	if embed.External != nil {
		// Use Bluesky's pre-fetched metadata if available
		if embed.External.Title != "" {
			urlCount += p.processExternalWithMetadata(
				postURI,
				embed.External.URI,
				embed.External.Title,
				embed.External.Description,
				embed.External.Thumb,
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
		urls := urlutil.ExtractURLs(quotedPost.Record.Text)
		urlCount += p.processURLs(postURI, urls)

		// Recursively process embeds in the quoted post
		if quotedPost.Embed != nil {
			urlCount += p.processEmbed(postURI, quotedPost.Embed)
		}
	}

	return urlCount
}

// processExternalWithMetadata processes an external link with pre-fetched metadata from Bluesky
func (p *Poller) processExternalWithMetadata(postURI, rawURL, title, description, imageURL string) int {
	// Normalize URL
	normalizedURL, err := urlutil.Normalize(rawURL)
	if err != nil {
		log.Printf("Error normalizing URL %s: %v", rawURL, err)
		return 0
	}

	// Get or create link
	link, err := p.db.GetOrCreateLink(rawURL, normalizedURL)
	if err != nil {
		log.Printf("Error with link %s: %v", rawURL, err)
		return 0
	}

	// Link post to link
	if err := p.db.LinkPostToLink(postURI, link.ID); err != nil {
		log.Printf("Error linking post to link: %v", err)
		return 0
	}

	// Store Bluesky's metadata if we don't have any yet
	if link.Title == nil {
		if err := p.db.UpdateLinkMetadata(link.ID, title, description, imageURL); err != nil {
			log.Printf("Error updating link metadata: %v", err)
		}
	}

	return 1
}

// fetchOGDataAsync fetches OpenGraph data in the background
func (p *Poller) fetchOGDataAsync(linkID int, url string) {
	ogData, err := p.scraper.FetchOGData(url)
	if err != nil {
		log.Printf("Error fetching OG data for %s: %v", url, err)
		return
	}

	// Update link with OG data
	if err := p.db.UpdateLinkMetadata(linkID, ogData.Title, ogData.Description, ogData.ImageURL); err != nil {
		log.Printf("Error updating link metadata: %v", err)
	}
}
