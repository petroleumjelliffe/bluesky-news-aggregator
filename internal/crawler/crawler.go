package crawler

import (
	"context"
	"fmt"
	"log"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

// Crawler crawls the extended network to discover 2nd-degree connections
type Crawler struct {
	db          *database.DB
	bskyClient  *bluesky.Client
	rateLimiter *RateLimiter
	myDID       string // The authenticated user's DID
}

// Config holds crawler configuration
type Config struct {
	RequestsPerSecond int
	SourceCountMin    int // Minimum number of 1st-degree accounts that must follow a 2nd-degree account
}

// Candidate represents a potential 2nd-degree account
type Candidate struct {
	DID         string
	Handle      string
	DisplayName string
	AvatarURL   string
	SourceCount int
	SourceDIDs  []string
}

// NewCrawler creates a new network crawler
func NewCrawler(db *database.DB, bskyClient *bluesky.Client, myDID string, config *Config) *Crawler {
	if config.RequestsPerSecond == 0 {
		config.RequestsPerSecond = 10 // Safe default
	}
	if config.SourceCountMin == 0 {
		config.SourceCountMin = 2 // Only keep 2nd-degree accounts followed by 2+ of your follows
	}

	return &Crawler{
		db:          db,
		bskyClient:  bskyClient,
		rateLimiter: NewRateLimiter(config.RequestsPerSecond),
		myDID:       myDID,
	}
}

// CrawlSecondDegree crawls 1st-degree follows to build a 2nd-degree network map
func (c *Crawler) CrawlSecondDegree(ctx context.Context, sourceCountMin int) error {
	log.Printf("[INFO] Starting 2nd-degree network crawl (min source count: %d)", sourceCountMin)

	// Step 1: Get all 1st-degree follows from the database
	firstDegree, err := c.db.GetNetworkAccountsByDegree(1, 0)
	if err != nil {
		return fmt.Errorf("failed to get 1st-degree accounts: %w", err)
	}

	log.Printf("[INFO] Found %d 1st-degree accounts to crawl", len(firstDegree))

	// Step 2: Track 2nd-degree candidates
	candidates := make(map[string]*Candidate)
	firstDegreeMap := make(map[string]bool)

	// Build map of 1st-degree DIDs for quick lookup
	for _, account := range firstDegree {
		firstDegreeMap[account.DID] = true
	}

	// Step 3: For each 1st-degree account, fetch who they follow
	for i, account := range firstDegree {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("[INFO] [%d/%d] Fetching follows for %s (%s)", i+1, len(firstDegree), account.Handle, account.DID)

		// Rate limit
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return err
		}

		// Fetch their follows
		theirFollows, err := c.bskyClient.GetFollowsWithMetadata(account.Handle)
		if err != nil {
			log.Printf("[WARN] Failed to get follows for %s: %v", account.Handle, err)
			continue
		}

		log.Printf("[INFO] %s follows %d accounts", account.Handle, len(theirFollows))

		// Process each follow
		for _, follow := range theirFollows {
			// Skip if this is a 1st-degree account
			if firstDegreeMap[follow.DID] {
				continue
			}

			// Skip self
			if follow.DID == c.myDID {
				continue
			}

			// Add or update candidate
			if existing, ok := candidates[follow.DID]; ok {
				existing.SourceCount++
				existing.SourceDIDs = append(existing.SourceDIDs, account.DID)
			} else {
				candidates[follow.DID] = &Candidate{
					DID:         follow.DID,
					Handle:      follow.Handle,
					DisplayName: follow.DisplayName,
					AvatarURL:   follow.Avatar,
					SourceCount: 1,
					SourceDIDs:  []string{account.DID},
				}
			}
		}

		log.Printf("[INFO] Current candidates: %d (after processing %s)", len(candidates), account.Handle)
	}

	// Step 4: Filter and save candidates
	log.Printf("[INFO] Filtering %d candidates (min source count: %d)", len(candidates), sourceCountMin)

	saved := 0
	for _, candidate := range candidates {
		if candidate.SourceCount >= sourceCountMin {
			// Prepare optional fields
			var displayName *string
			if candidate.DisplayName != "" {
				displayName = &candidate.DisplayName
			}
			var avatarURL *string
			if candidate.AvatarURL != "" {
				avatarURL = &candidate.AvatarURL
			}

			// Save to database
			err := c.db.UpsertNetworkAccount(
				candidate.DID,
				candidate.Handle,
				displayName,
				avatarURL,
				2, // degree
				candidate.SourceCount,
				candidate.SourceDIDs,
			)
			if err != nil {
				log.Printf("[WARN] Failed to save candidate %s: %v", candidate.Handle, err)
				continue
			}
			saved++
		}
	}

	log.Printf("[INFO] Saved %d 2nd-degree accounts (filtered from %d candidates)", saved, len(candidates))

	return nil
}

// SyncFirstDegree syncs 1st-degree follows from the API to the database
func (c *Crawler) SyncFirstDegree(ctx context.Context, myHandle string) error {
	log.Printf("[INFO] Syncing 1st-degree follows for %s", myHandle)

	// Rate limit
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return err
	}

	// Fetch follows from API
	follows, err := c.bskyClient.GetFollowsWithMetadata(myHandle)
	if err != nil {
		return fmt.Errorf("failed to get follows: %w", err)
	}

	log.Printf("[INFO] Found %d 1st-degree follows", len(follows))

	// Save each to network_accounts table
	for _, follow := range follows {
		var displayName *string
		if follow.DisplayName != "" {
			displayName = &follow.DisplayName
		}
		var avatarURL *string
		if follow.Avatar != "" {
			avatarURL = &follow.Avatar
		}

		err := c.db.UpsertNetworkAccount(
			follow.DID,
			follow.Handle,
			displayName,
			avatarURL,
			1, // degree
			1, // source_count (you follow them directly)
			[]string{c.myDID},
		)
		if err != nil {
			log.Printf("[WARN] Failed to save 1st-degree account %s: %v", follow.Handle, err)
		}
	}

	log.Printf("[INFO] Synced %d 1st-degree accounts", len(follows))

	return nil
}

// GetStats returns network statistics
func (c *Crawler) GetStats() (map[string]interface{}, error) {
	return c.db.GetNetworkStats()
}

// Close cleans up resources
func (c *Crawler) Close() {
	c.rateLimiter.Close()
}
