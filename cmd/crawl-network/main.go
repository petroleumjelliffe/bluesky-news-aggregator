package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/crawler"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database"
)

func main() {
	// Parse flags
	degree := flag.Int("degree", 2, "Network degree to crawl (2 = 2nd-degree)")
	threshold := flag.Int("threshold", 2, "Minimum source count for 2nd-degree accounts")
	statsOnly := flag.Bool("stats", false, "Only show network statistics")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database
	log.Printf("[INFO] Connecting to database: %s", cfg.Database.DatabaseConnStringSafe())
	db, err := database.NewDB(cfg.Database.DatabaseConnString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// If stats only, print and exit
	if *statsOnly {
		printStats(db)
		return
	}

	// Create Bluesky client
	log.Printf("[INFO] Authenticating with Bluesky as %s", cfg.Bluesky.Handle)
	bskyClient, err := bluesky.NewClient(cfg.Bluesky.Handle, cfg.Bluesky.Password)
	if err != nil {
		log.Fatalf("Failed to create Bluesky client: %v", err)
	}

	// Get my DID from authenticated session
	myDID := bskyClient.GetDID()
	log.Printf("[INFO] My DID: %s", myDID)

	// Create crawler
	crawlerConfig := &crawler.Config{
		RequestsPerSecond: 10,
		SourceCountMin:    *threshold,
	}
	c := crawler.NewCrawler(db, bskyClient, myDID, crawlerConfig)
	defer c.Close()

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Printf("[INFO] Interrupt received, stopping...")
		cancel()
	}()

	// Step 1: Sync 1st-degree follows
	log.Printf("[INFO] ========== Syncing 1st-degree follows ==========")
	if err := c.SyncFirstDegree(ctx, cfg.Bluesky.Handle); err != nil {
		log.Fatalf("Failed to sync 1st-degree: %v", err)
	}

	// Step 2: Crawl 2nd-degree network (if requested)
	if *degree >= 2 {
		log.Printf("[INFO] ========== Crawling 2nd-degree network ==========")
		if err := c.CrawlSecondDegree(ctx, *threshold); err != nil {
			log.Fatalf("Failed to crawl 2nd-degree: %v", err)
		}
	}

	// Step 3: Show stats
	log.Printf("[INFO] ========== Network Statistics ==========")
	printStats(db)

	log.Printf("[INFO] Crawl complete!")
}

func printStats(db *database.DB) {
	stats, err := db.GetNetworkStats()
	if err != nil {
		log.Printf("[ERROR] Failed to get stats: %v", err)
		return
	}

	fmt.Println("\nNetwork Statistics:")
	fmt.Printf("  1st-degree (direct follows):     %d\n", stats["first_degree"])
	fmt.Printf("  2nd-degree (all):                 %d\n", stats["second_degree"])
	fmt.Printf("  2nd-degree (2+ sources):          %d\n", stats["second_degree_2plus"])
	fmt.Printf("  2nd-degree (3+ sources):          %d\n", stats["second_degree_3plus"])
	fmt.Println()
}
