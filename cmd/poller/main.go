package main

import (
	"fmt"
	"log"
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
	DatabaseURL      string
	BlueskyHandle    string
	BlueskyPassword  string
	PollingInterval  time.Duration
	MaxConcurrent    int
	RateLimitMS      int
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
		DatabaseURL:      dbURL,
		BlueskyHandle:    viper.GetString("bluesky.handle"),
		BlueskyPassword:  viper.GetString("bluesky.password"),
		PollingInterval:  time.Duration(viper.GetInt("polling.interval_minutes")) * time.Minute,
		MaxConcurrent:    viper.GetInt("polling.max_concurrent"),
		RateLimitMS:      viper.GetInt("polling.rate_limit_ms"),
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
	// Get last cursor
	cursor, err := p.db.GetLastCursor(handle)
	if err != nil {
		log.Printf("Error getting cursor for %s: %v", handle, err)
		return
	}

	// Fetch posts
	feed, err := p.bskyClient.GetAuthorFeed(handle, cursor, 50)
	if err != nil {
		log.Printf("Error fetching feed for %s: %v", handle, err)
		return
	}

	if len(feed.Feed) == 0 {
		return
	}

	log.Printf("Processing %d posts from %s", len(feed.Feed), handle)

	for _, item := range feed.Feed {
		p.processPost(&item.Post)
	}

	// Update cursor
	if feed.Cursor != "" {
		if err := p.db.UpdateCursor(handle, feed.Cursor); err != nil {
			log.Printf("Error updating cursor for %s: %v", handle, err)
		}
	}
}

// processPost extracts URLs and stores the post
func (p *Poller) processPost(post *bluesky.Post) {
	// Insert post
	dbPost := &database.Post{
		ID:           post.URI,
		AuthorHandle: post.Author.Handle,
		Content:      post.Record.Text,
		CreatedAt:    post.Record.CreatedAt,
	}

	if err := p.db.InsertPost(dbPost); err != nil {
		log.Printf("Error inserting post %s: %v", post.URI, err)
		return
	}

	// Extract URLs from post text
	urls := urlutil.ExtractURLs(post.Record.Text)

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
		if err := p.db.LinkPostToLink(post.URI, link.ID); err != nil {
			log.Printf("Error linking post to link: %v", err)
			continue
		}

		// Fetch OG data if not already fetched
		if link.Title == nil {
			go p.fetchOGDataAsync(link.ID, normalizedURL)
		}
	}
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
