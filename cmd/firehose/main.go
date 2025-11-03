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
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/jetstream"
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

	// For now, just test with a simple handler that logs events
	handler := func(ctx context.Context, event *models.Event) error {
		// Only log commit events for now
		if event.Kind == "commit" && event.Commit != nil {
			if event.Commit.Operation == "create" && event.Commit.Collection == "app.bsky.feed.post" {
				log.Printf("[EVENT] Post created by %s: %s", event.Did, event.Commit.RKey)
			}
		}
		return nil
	}

	// Create Jetstream client
	// TODO: Get actual DIDs from follows table
	client, err := jetstream.NewClient(&jetstream.Config{
		WebsocketURL:      "wss://jetstream2.us-west.bsky.network/subscribe",
		Compress:          true,
		WantedCollections: []string{"app.bsky.feed.post"},
		WantedDIDs:        []string{}, // Empty = all posts (for testing)
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

	// Connect and read events
	if err := client.Connect(ctx, nil); err != nil {
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
