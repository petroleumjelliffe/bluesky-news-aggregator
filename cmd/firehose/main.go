package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/didmanager"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/jetstream"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/processor"
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

	log.Printf("[INFO] Starting Jetstream firehose consumer...")

	// Create DID manager and load follows
	didManager := didmanager.NewManager(db)
	if err := didManager.LoadFromDatabase(); err != nil {
		log.Fatalf("Failed to load follows: %v", err)
	}

	log.Printf("[INFO] Filtering to %d followed DIDs", didManager.Count())

	// Load last cursor for crash recovery
	lastCursor, err := db.GetJetstreamCursor()
	if err != nil {
		log.Fatalf("Failed to get last cursor: %v", err)
	}

	if lastCursor != nil {
		log.Printf("[INFO] Resuming from cursor: %d", *lastCursor)
	} else {
		log.Printf("[INFO] Starting from current time (no previous cursor)")
	}

	// Create processor for handling events
	proc := processor.NewProcessor(db)

	// Event handler that processes filtered events
	handler := func(ctx context.Context, event *models.Event) error {
		// Only process commit events for posts
		if event.Kind == "commit" && event.Commit != nil {
			if event.Commit.Operation == "create" && event.Commit.Collection == "app.bsky.feed.post" {
				// Update last_seen_at for this DID
				if err := db.UpdateFollowLastSeen(event.Did); err != nil {
					log.Printf("[WARN] Failed to update last_seen for %s: %v", event.Did, err)
				}

				// Process the post (extract URLs, store in DB, fetch metadata)
				if err := proc.ProcessEvent(event); err != nil {
					log.Printf("[ERROR] Failed to process event: %v", err)
					return err
				}
			}
		}

		// Update cursor periodically (every event for now, could batch)
		if err := db.UpdateJetstreamCursor(event.TimeUS); err != nil {
			log.Printf("[WARN] Failed to update cursor: %v", err)
		}

		return nil
	}

	// Create Jetstream client with DID filtering
	client, err := jetstream.NewClient(&jetstream.Config{
		WebsocketURL:      "wss://jetstream2.us-west.bsky.network/subscribe",
		Compress:          true,
		WantedCollections: []string{"app.bsky.feed.post"},
		WantedDIDs:        didManager.GetDIDs(),
	}, handler)
	if err != nil {
		log.Fatalf("Failed to create Jetstream client: %v", err)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Printf("[INFO] Shutdown signal received, stopping...")
		cancel()
	}()

	// Start stats reporter
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				bytes, events := client.Stats()
				log.Printf("[STATS] Events: %d, Bytes: %s", events, formatBytes(bytes))
			}
		}
	}()

	// Connect and read events (resume from cursor if available)
	if err := client.Connect(ctx, lastCursor); err != nil {
		log.Fatalf("Failed to connect to Jetstream: %v", err)
	}

	log.Printf("[INFO] Firehose consumer stopped")
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
