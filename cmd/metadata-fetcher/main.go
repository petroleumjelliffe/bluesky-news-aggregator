package main

import (
	"fmt"
	"log"
	"time"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/scraper"
	"github.com/spf13/viper"
)

// Config holds metadata fetcher configuration
type Config struct {
	DatabaseURL   string
	MaxConcurrent int
	RateLimitMS   int
	MaxRetries    int
	DryRun        bool
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

	log.Printf("[INFO] Starting metadata fetcher...")
	if config.DryRun {
		log.Printf("[INFO] DRY RUN MODE - No changes will be made")
	}

	// Create scraper
	sc := scraper.NewScraper()

	// Get links that need metadata
	links, err := getLinksNeedingMetadata(db)
	if err != nil {
		log.Fatalf("Failed to get links: %v", err)
	}

	log.Printf("[INFO] Found %d links without metadata", len(links))

	if len(links) == 0 {
		log.Printf("[INFO] No links need metadata fetching. Exiting.")
		return
	}

	// Process links
	successCount := 0
	failureCount := 0
	skippedCount := 0

	for i, link := range links {
		log.Printf("[%d/%d] Processing: %s", i+1, len(links), link.NormalizedURL)

		// Skip if dry run
		if config.DryRun {
			skippedCount++
			continue
		}

		// Fetch metadata
		ogData, err := sc.FetchOGData(link.NormalizedURL)
		if err != nil {
			log.Printf("[WARN] Failed to fetch metadata for %s: %v", link.NormalizedURL, err)
			failureCount++

			// Mark as fetched even on failure to avoid retry storms
			if err := db.MarkLinkFetched(link.ID); err != nil {
				log.Printf("[ERROR] Failed to mark link as fetched: %v", err)
			}
			continue
		}

		// Update metadata
		if err := db.UpdateLinkMetadata(link.ID, ogData.Title, ogData.Description, ogData.ImageURL); err != nil {
			log.Printf("[ERROR] Failed to update metadata for %s: %v", link.NormalizedURL, err)
			failureCount++
			continue
		}

		successCount++
		log.Printf("[SUCCESS] Updated metadata for %s (title: %q)", link.NormalizedURL, ogData.Title)

		// Rate limiting
		time.Sleep(time.Duration(config.RateLimitMS) * time.Millisecond)
	}

	log.Printf("[INFO] Metadata fetching complete!")
	log.Printf("[INFO] Results: %d succeeded, %d failed, %d skipped", successCount, failureCount, skippedCount)
}

func loadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./config")
	viper.AddConfigPath(".")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

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

	return &Config{
		DatabaseURL:   dbURL,
		MaxConcurrent: 5,
		RateLimitMS:   1000, // 1 second between requests
		MaxRetries:    2,
		DryRun:        false,
	}, nil
}

// getLinksNeedingMetadata retrieves links without metadata that haven't been fetched yet
func getLinksNeedingMetadata(db *database.DB) ([]database.Link, error) {
	query := `
		SELECT id, normalized_url, original_url, title, description, og_image_url
		FROM links
		WHERE title IS NULL
		AND last_fetched_at IS NULL
		ORDER BY first_seen_at DESC
		LIMIT 500
	`

	var links []database.Link
	err := db.Select(&links, query)
	return links, err
}
