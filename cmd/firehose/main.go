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
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/didmanager"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/jetstream"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/maintenance"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/processor"
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

	log.Printf("[INFO] Starting Jetstream firehose consumer...")

	// Load cleanup configuration
	cleanupConfig := maintenance.Config{
		RetentionHours:       cfg.Cleanup.RetentionHours,
		TrendingThreshold:    cfg.Cleanup.TrendingThreshold,
		CleanupIntervalMin:   cfg.Cleanup.CleanupIntervalMin,
		CursorUpdateInterval: cfg.Cleanup.CursorUpdateSeconds,
	}

	// PHASE 1: Startup cleanup
	if err := maintenance.StartupCleanup(db, cleanupConfig); err != nil {
		log.Fatalf("Startup cleanup failed: %v", err)
	}

	// Create DID manager and load follows
	// Enable 2nd-degree filtering with minimum 2 sources
	didManager := didmanager.NewManagerWithConfig(db, &didmanager.Config{
		Include2ndDegree: true,
		MinSourceCount:   2,
	})
	if err := didManager.LoadFromDatabase(); err != nil {
		log.Fatalf("Failed to load follows: %v", err)
	}

	counts := didManager.CountByDegree()
	log.Printf("[INFO] Filtering to %d DIDs (%d 1st-degree, %d 2nd-degree)",
		didManager.Count(), counts[1], counts[2])

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

	// Create processor for handling events (with DID manager for degree lookup)
	proc := processor.NewProcessor(db, didManager)

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
				// LOCAL FILTER: Only process posts from accounts we follow
				// We filter client-side because 300+ DIDs in the WebSocket URL exceeds length limits
				if !didManager.IsFollowed(event.Did) {
					return nil // Skip posts from accounts we don't follow
				}

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

	// Create Jetstream client (filtering is done client-side to avoid URL length limits)
	client, err := jetstream.NewClient(&jetstream.Config{
		WebsocketURL:      "wss://jetstream2.us-west.bsky.network/subscribe",
		Compress:          true,
		WantedCollections: []string{"app.bsky.feed.post"},
		// Note: WantedDIDs removed - 300+ DIDs exceeds WebSocket URL length limit
		// Filtering is done client-side in the handler using didManager.IsFollowed()
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
