// Package maintenance provides database cleanup and maintenance procedures.
package maintenance

import (
	"fmt"
	"log"
	"time"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// Config holds cleanup configuration
type Config struct {
	RetentionHours       int // How long to keep data
	TrendingThreshold    int // Minimum shares to keep a link regardless of age
	CleanupIntervalMin   int // How often to run periodic cleanup
	CursorUpdateInterval int // Seconds between cursor updates
}

// StartupCleanup performs database cleanup on service startup
// This ensures we start with a clean slate and remove stale data
func StartupCleanup(db *database.DB, config Config) error {
	log.Println("[STARTUP] Running cleanup procedures...")
	startTime := time.Now()

	cutoff := time.Now().Add(-time.Duration(config.RetentionHours) * time.Hour)
	log.Printf("[STARTUP] Cutoff time: %v (%dh ago)", cutoff, config.RetentionHours)

	// 1. Delete posts older than retention period
	postsDeleted, err := db.DeleteOldPosts(cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old posts: %w", err)
	}
	log.Printf("[STARTUP] ✓ Deleted %d old posts (>%dh)", postsDeleted, config.RetentionHours)

	// 2. Delete orphaned post_links (safety cleanup)
	orphansDeleted, err := db.DeleteOrphanedPostLinks()
	if err != nil {
		return fmt.Errorf("failed to delete orphaned links: %w", err)
	}
	if orphansDeleted > 0 {
		log.Printf("[STARTUP] ✓ Deleted %d orphaned post_links", orphansDeleted)
	}

	// 3. Delete links with no recent shares (except trending)
	linksDeleted, err := db.DeleteUnsharedLinks(cutoff, config.TrendingThreshold)
	if err != nil {
		return fmt.Errorf("failed to delete unshared links: %w", err)
	}
	log.Printf("[STARTUP] ✓ Deleted %d unshared links (keeping trending with %d+ shares)",
		linksDeleted, config.TrendingThreshold)

	duration := time.Since(startTime)
	log.Printf("[STARTUP] Cleanup complete in %v", duration)
	return nil
}

// PeriodicCleanup runs ongoing cleanup during service operation
func PeriodicCleanup(db *database.DB, config Config) error {
	log.Println("[CLEANUP] Running periodic cleanup...")
	startTime := time.Now()

	cutoff := time.Now().Add(-time.Duration(config.RetentionHours) * time.Hour)

	// 1. Delete old posts
	postsDeleted, err := db.DeleteOldPosts(cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old posts: %w", err)
	}

	// 2. Delete unshared links (except trending)
	linksDeleted, err := db.DeleteUnsharedLinks(cutoff, config.TrendingThreshold)
	if err != nil {
		return fmt.Errorf("failed to delete unshared links: %w", err)
	}

	duration := time.Since(startTime)
	log.Printf("[CLEANUP] Deleted %d posts, %d links in %v", postsDeleted, linksDeleted, duration)
	return nil
}

// StartCleanupTicker starts a background goroutine that runs periodic cleanup
func StartCleanupTicker(db *database.DB, config Config) {
	if config.CleanupIntervalMin <= 0 {
		log.Println("[CLEANUP] Periodic cleanup disabled (interval <= 0)")
		return
	}

	interval := time.Duration(config.CleanupIntervalMin) * time.Minute
	ticker := time.NewTicker(interval)

	go func() {
		log.Printf("[CLEANUP] Started periodic cleanup (interval: %v)", interval)
		for range ticker.C {
			if err := PeriodicCleanup(db, config); err != nil {
				log.Printf("[CLEANUP] Error: %v", err)
			}
		}
	}()
}
