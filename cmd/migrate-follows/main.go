package main

import (
	"fmt"
	"log"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/spf13/viper"
)

func main() {
	// Load configuration
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("config")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}

	// Connect to database
	password := viper.GetString("database.password")
	var connStr string
	if password == "" {
		connStr = fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	} else {
		connStr = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			viper.GetString("database.host"),
			viper.GetInt("database.port"),
			viper.GetString("database.user"),
			password,
			viper.GetString("database.dbname"),
			viper.GetString("database.sslmode"),
		)
	}

	db, err := database.NewDB(connStr)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create Bluesky client
	client, err := bluesky.NewClient(
		viper.GetString("bluesky.handle"),
		viper.GetString("bluesky.password"),
	)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	log.Printf("[INFO] Migrating follows from poll_state to follows table...")

	// Get current follows from GetFollows API
	handles, err := client.GetFollows(viper.GetString("bluesky.handle"))
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

		// Extract DID from first post
		did := feed.Feed[0].Post.Author.DID
		displayName := &feed.Feed[0].Post.Author.DisplayName

		// Insert into follows table
		if err := db.AddFollow(did, handle, displayName); err != nil {
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
