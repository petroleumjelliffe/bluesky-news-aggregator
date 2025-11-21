package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bluesky-social/jetstream/pkg/models"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/didmanager"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/jetstream"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/maintenance"
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

	// Load cleanup configuration
	cleanupConfig := maintenance.Config{
		RetentionHours:       viper.GetInt("cleanup.retention_hours"),
		TrendingThreshold:    viper.GetInt("cleanup.trending_threshold"),
		CleanupIntervalMin:   viper.GetInt("cleanup.cleanup_interval_minutes"),
		CursorUpdateInterval: viper.GetInt("cleanup.cursor_update_seconds"),
	}

	// Set defaults if not configured
	if cleanupConfig.RetentionHours == 0 {
		cleanupConfig.RetentionHours = 24
	}
	if cleanupConfig.TrendingThreshold == 0 {
		cleanupConfig.TrendingThreshold = 5
	}
	if cleanupConfig.CleanupIntervalMin == 0 {
		cleanupConfig.CleanupIntervalMin = 60
	}
	if cleanupConfig.CursorUpdateInterval == 0 {
		cleanupConfig.CursorUpdateInterval = 10
	}

	// PHASE 1: Startup cleanup
	if err := maintenance.StartupCleanup(db, cleanupConfig); err != nil {
		log.Fatalf("Startup cleanup failed: %v", err)
	}

	// Create DID manager and load follows
	didManager := didmanager.NewManager(db)
	if err := didManager.LoadFromDatabase(); err != nil {
		log.Fatalf("Failed to load follows: %v", err)
	}

	log.Printf("[INFO] Filtering to %d followed DIDs", didManager.Count())

	// Load last cursor for crash recovery
	savedCursor, err := db.GetJetstreamCursor()
	if err != nil {
		log.Fatalf("Failed to get last cursor: %v", err)
	}

	if savedCursor != nil {
		log.Printf("[INFO] Resuming from cursor: %d", *savedCursor)
	} else {
		log.Printf("[INFO] Starting from current time (no previous cursor)")
	}

	// PHASE 3: Start periodic cleanup ticker
	maintenance.StartCleanupTicker(db, cleanupConfig)

	// Create processor for handling events
	proc := processor.NewProcessor(db)

	// Cursor batching variables
	var (
		currentCursor    int64
		lastCursorUpdate time.Time
		cursorMutex      sync.Mutex
	)

	cursorUpdateInterval := time.Duration(cleanupConfig.CursorUpdateInterval) * time.Second

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

		// Update cursor in memory (batched writes to database)
		cursorMutex.Lock()
		currentCursor = event.TimeUS
		cursorMutex.Unlock()

		// Periodically flush cursor to database (every N seconds instead of every event)
		cursorMutex.Lock()
		shouldUpdate := time.Since(lastCursorUpdate) > cursorUpdateInterval
		cursorMutex.Unlock()

		if shouldUpdate {
			cursorMutex.Lock()
			cursor := currentCursor
			cursorMutex.Unlock()

			if err := db.UpdateJetstreamCursor(cursor); err != nil {
				log.Printf("[WARN] Failed to update cursor: %v", err)
			} else {
				cursorMutex.Lock()
				lastCursorUpdate = time.Now()
				cursorMutex.Unlock()
			}
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

	// Flush final cursor on shutdown
	defer func() {
		cursorMutex.Lock()
		cursor := currentCursor
		cursorMutex.Unlock()

		if cursor > 0 {
			if err := db.UpdateJetstreamCursor(cursor); err != nil {
				log.Printf("[ERROR] Failed to save final cursor: %v", err)
			} else {
				log.Printf("[INFO] Final cursor saved: %d", cursor)
			}
		}
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
	if err := client.Connect(ctx, savedCursor); err != nil {
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
