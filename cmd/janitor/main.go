package main

import (
	"fmt"
	"log"
	"time"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// JanitorConfig holds janitor-specific configuration
type JanitorConfig struct {
	PostRetentionDays int
	LinkRetentionDays int
	DryRun            bool
}

func main() {
	// Load configuration (supports env vars)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize database (log safe connection string without password)
	log.Printf("[INFO] Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := database.NewDB(cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Default retention periods (can be overridden later if needed)
	janitorCfg := &JanitorConfig{
		PostRetentionDays: 30,
		LinkRetentionDays: 90,
		DryRun:            false,
	}

	log.Printf("[INFO] Starting database cleanup...")
	if janitorCfg.DryRun {
		log.Printf("[INFO] DRY RUN MODE - No changes will be made")
	}

	// Clean up old posts
	if err := cleanupOldPosts(db, janitorCfg); err != nil {
		log.Fatalf("Failed to clean up posts: %v", err)
	}

	// Clean up orphaned links (links with no post_links references)
	if err := cleanupOrphanedLinks(db, janitorCfg); err != nil {
		log.Fatalf("Failed to clean up orphaned links: %v", err)
	}

	// Clean up old links (based on last shared date)
	if err := cleanupOldLinks(db, janitorCfg); err != nil {
		log.Fatalf("Failed to clean up old links: %v", err)
	}

	log.Printf("[INFO] Database cleanup complete!")
}

// cleanupOldPosts removes posts older than the retention period
func cleanupOldPosts(db *database.DB, cfg *JanitorConfig) error {
	cutoff := time.Now().AddDate(0, 0, -cfg.PostRetentionDays)

	log.Printf("[INFO] Cleaning up posts older than %d days (before %s)...", cfg.PostRetentionDays, cutoff.Format("2006-01-02"))

	// First, count how many posts will be deleted
	var count int
	countQuery := `SELECT COUNT(*) FROM posts WHERE created_at < $1`
	if err := db.Get(&count, countQuery, cutoff); err != nil {
		return fmt.Errorf("failed to count old posts: %w", err)
	}

	log.Printf("[INFO] Found %d posts to delete", count)

	if count == 0 {
		log.Printf("[INFO] No old posts to clean up")
		return nil
	}

	if cfg.DryRun {
		log.Printf("[DRY RUN] Would delete %d posts", count)
		return nil
	}

	// Delete post_links references first
	deletePostLinksQuery := `
		DELETE FROM post_links
		WHERE post_id IN (
			SELECT id FROM posts WHERE created_at < $1
		)
	`
	result, err := db.Exec(deletePostLinksQuery, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete post_links: %w", err)
	}

	postLinksDeleted, _ := result.RowsAffected()
	log.Printf("[INFO] Deleted %d post_links references", postLinksDeleted)

	// Delete posts
	deletePostsQuery := `DELETE FROM posts WHERE created_at < $1`
	result, err = db.Exec(deletePostsQuery, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete posts: %w", err)
	}

	postsDeleted, _ := result.RowsAffected()
	log.Printf("[INFO] Deleted %d posts", postsDeleted)

	return nil
}

// cleanupOrphanedLinks removes links that are no longer referenced by any posts
func cleanupOrphanedLinks(db *database.DB, cfg *JanitorConfig) error {
	log.Printf("[INFO] Cleaning up orphaned links (no post references)...")

	// Count orphaned links
	var count int
	countQuery := `
		SELECT COUNT(*)
		FROM links l
		WHERE NOT EXISTS (
			SELECT 1 FROM post_links pl WHERE pl.link_id = l.id
		)
	`
	if err := db.Get(&count, countQuery); err != nil {
		return fmt.Errorf("failed to count orphaned links: %w", err)
	}

	log.Printf("[INFO] Found %d orphaned links", count)

	if count == 0 {
		log.Printf("[INFO] No orphaned links to clean up")
		return nil
	}

	if cfg.DryRun {
		log.Printf("[DRY RUN] Would delete %d orphaned links", count)
		return nil
	}

	// Delete orphaned links
	deleteQuery := `
		DELETE FROM links
		WHERE NOT EXISTS (
			SELECT 1 FROM post_links pl WHERE pl.link_id = links.id
		)
	`
	result, err := db.Exec(deleteQuery)
	if err != nil {
		return fmt.Errorf("failed to delete orphaned links: %w", err)
	}

	deleted, _ := result.RowsAffected()
	log.Printf("[INFO] Deleted %d orphaned links", deleted)

	return nil
}

// cleanupOldLinks removes links that haven't been shared recently
func cleanupOldLinks(db *database.DB, cfg *JanitorConfig) error {
	cutoff := time.Now().AddDate(0, 0, -cfg.LinkRetentionDays)

	log.Printf("[INFO] Cleaning up links not shared since %d days ago (before %s)...", cfg.LinkRetentionDays, cutoff.Format("2006-01-02"))

	// Count old links (links where the most recent post is older than cutoff)
	var count int
	countQuery := `
		SELECT COUNT(DISTINCT l.id)
		FROM links l
		INNER JOIN post_links pl ON l.id = pl.link_id
		INNER JOIN posts p ON pl.post_id = p.id
		GROUP BY l.id
		HAVING MAX(p.created_at) < $1
	`
	if err := db.Get(&count, countQuery, cutoff); err != nil {
		// Query might fail if no results, which is fine
		count = 0
	}

	log.Printf("[INFO] Found %d old links to delete", count)

	if count == 0 {
		log.Printf("[INFO] No old links to clean up")
		return nil
	}

	if cfg.DryRun {
		log.Printf("[DRY RUN] Would delete %d old links and their post_links", count)
		return nil
	}

	// Delete post_links for old links
	deletePostLinksQuery := `
		DELETE FROM post_links
		WHERE link_id IN (
			SELECT l.id
			FROM links l
			INNER JOIN post_links pl2 ON l.id = pl2.link_id
			INNER JOIN posts p ON pl2.post_id = p.id
			GROUP BY l.id
			HAVING MAX(p.created_at) < $1
		)
	`
	result, err := db.Exec(deletePostLinksQuery, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete post_links for old links: %w", err)
	}

	postLinksDeleted, _ := result.RowsAffected()
	log.Printf("[INFO] Deleted %d post_links for old links", postLinksDeleted)

	// Delete the links themselves
	deleteLinksQuery := `
		DELETE FROM links
		WHERE id IN (
			SELECT l.id
			FROM links l
			LEFT JOIN post_links pl ON l.id = pl.link_id
			LEFT JOIN posts p ON pl.post_id = p.id
			GROUP BY l.id
			HAVING MAX(p.created_at) < $1 OR MAX(p.created_at) IS NULL
		)
	`
	result, err = db.Exec(deleteLinksQuery, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old links: %w", err)
	}

	linksDeleted, _ := result.RowsAffected()
	log.Printf("[INFO] Deleted %d old links", linksDeleted)

	return nil
}
