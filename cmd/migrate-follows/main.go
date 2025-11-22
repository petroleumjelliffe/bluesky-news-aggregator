package main

import (
	"log"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

func main() {
	// Load configuration (supports env vars)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database (log safe connection string without password)
	log.Printf("[INFO] Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := database.NewDB(cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create Bluesky client
	client, err := bluesky.NewClient(cfg.Bluesky.Handle, cfg.Bluesky.Password)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	log.Printf("[INFO] Migrating follows from poll_state to follows table...")

	// Get current follows from GetFollows API
	handles, err := client.GetFollows(cfg.Bluesky.Handle)
	if err != nil {
		log.Fatalf("Failed to get follows: %v", err)
	}

	log.Printf("[INFO] Found %d follows from API", len(handles))

	// For each handle, resolve to DID and insert into follows table
	successCount := 0
	for i, handle := range handles {
		// Use a simple API call to resolve handle to DID
		// The GetAuthorFeed response includes the DID
		feed, err := client.GetAuthorFeed(handle, "", 1)
		if err != nil {
			log.Printf("[WARN] Failed to resolve handle %s: %v", handle, err)
			continue
		}

		if len(feed.Feed) == 0 {
			log.Printf("[WARN] No posts found for %s, skipping", handle)
			continue
		}

		// Extract DID and avatar from first post
		did := feed.Feed[0].Post.Author.DID
		displayName := &feed.Feed[0].Post.Author.DisplayName
		var avatarURL *string
		if feed.Feed[0].Post.Author.Avatar != "" {
			avatarURL = &feed.Feed[0].Post.Author.Avatar
		}

		// Insert into follows table
		if err := db.AddFollow(did, handle, displayName, avatarURL); err != nil {
			log.Printf("[ERROR] Failed to add follow %s (%s): %v", handle, did, err)
			continue
		}

		successCount++
		if (i+1)%10 == 0 {
			log.Printf("[INFO] Progress: %d/%d handles processed", i+1, len(handles))
		}
	}

	log.Printf("[INFO] Migration complete: %d/%d follows added successfully", successCount, len(handles))
}
