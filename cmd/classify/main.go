package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/lib/pq"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/classifier"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/embeddings"
)

func main() {
	// Command-line flags
	var (
		limit               = flag.Int("limit", 20, "Number of recent links to classify")
		threshold           = flag.Float64("threshold", 0.80, "Similarity threshold (0-1) for grouping articles")
		minShares           = flag.Int("min-shares", 2, "Minimum number of shares for a link to be included")
		verbose             = flag.Bool("verbose", true, "Enable verbose logging")
		displayOnly         = flag.Bool("display-only", false, "Only display existing stories without running classification")
		runMigration        = flag.Bool("migrate", false, "Run database migration before classifying")
		providerType        = flag.String("provider", "ollama", "Embedding provider: 'ollama' or 'openai'")
		ollamaModel         = flag.String("ollama-model", "nomic-embed-text", "Ollama model to use")
		ollamaURL           = flag.String("ollama-url", "http://localhost:11434", "Ollama base URL")
	)
	flag.Parse()

	log.SetFlags(log.Ltime)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Connect to database
	db, err := connectDB(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migration if requested
	if *runMigration {
		if err := runDatabaseMigration(db); err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
		log.Println("✓ Migration completed successfully")
	}

	// Display existing stories and exit if display-only mode
	if *displayOnly {
		displayStories(db)
		return
	}

	// Initialize embedding provider based on flag
	var provider embeddings.Provider
	switch *providerType {
	case "ollama":
		log.Printf("Using Ollama provider (model: %s, url: %s)\n", *ollamaModel, *ollamaURL)
		provider = embeddings.NewOllamaProvider(*ollamaModel, *ollamaURL)
	case "openai":
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			log.Fatal("OPENAI_API_KEY environment variable is required for OpenAI provider")
		}
		log.Println("Using OpenAI provider (model: text-embedding-3-small)")
		provider = embeddings.NewOpenAIProvider(apiKey, "text-embedding-3-small")
	default:
		log.Fatalf("Unknown provider: %s (use 'ollama' or 'openai')", *providerType)
	}

	embeddingService := embeddings.NewEmbeddingService(provider)

	// Initialize classifier
	cls := classifier.NewClassifier(db, embeddingService, float32(*threshold))

	// Fetch recent links to classify
	log.Printf("Fetching up to %d recent links with at least %d shares...\n", *limit, *minShares)
	linkIDs, err := fetchRecentLinks(db, *limit, *minShares)
	if err != nil {
		log.Fatalf("Failed to fetch links: %v", err)
	}

	if len(linkIDs) == 0 {
		log.Println("No links found matching criteria")
		return
	}

	log.Printf("Found %d links to classify\n", len(linkIDs))
	log.Println(strings.Repeat("=", 70))

	// Run classification
	result, err := cls.ClassifyLinks(linkIDs, *verbose)
	if err != nil {
		log.Fatalf("Classification failed: %v", err)
	}

	// Display results
	log.Println(strings.Repeat("=", 70))
	log.Println("\n📊 CLASSIFICATION RESULTS")
	log.Println(strings.Repeat("=", 70))
	log.Printf("Duration:           %v", result.Duration)
	log.Printf("Articles processed: %d", result.ArticlesProcessed)
	log.Printf("Stories created:    %d", result.StoriesCreated)
	log.Printf("Similarity threshold: %.2f", *threshold)

	if result.StoriesCreated > 0 {
		log.Println(strings.Repeat("=", 70))
		log.Println("\n📰 DISCOVERED STORIES")
		log.Println(strings.Repeat("=", 70))
		displayStories(db)
	}
}

// connectDB establishes database connection
func connectDB(cfg *config.Config) (*sql.DB, error) {
	var connStr string
	if cfg.Database.Password == "" {
		connStr = fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			cfg.Database.Host,
			cfg.Database.Port,
			cfg.Database.User,
			cfg.Database.DBName,
			cfg.Database.SSLMode,
		)
	} else {
		connStr = fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			cfg.Database.Host,
			cfg.Database.Port,
			cfg.Database.User,
			cfg.Database.Password,
			cfg.Database.DBName,
			cfg.Database.SSLMode,
		)
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

// fetchRecentLinks fetches recent link IDs from the database
func fetchRecentLinks(db *sql.DB, limit, minShares int) ([]int, error) {
	query := `
		SELECT l.id
		FROM links l
		JOIN post_links pl ON l.id = pl.link_id
		GROUP BY l.id
		HAVING COUNT(pl.post_id) >= $1
		ORDER BY MAX(l.first_seen_at) DESC
		LIMIT $2
	`

	rows, err := db.Query(query, minShares, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var linkIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		linkIDs = append(linkIDs, id)
	}

	return linkIDs, rows.Err()
}

// displayStories displays existing story clusters
func displayStories(db *sql.DB) {
	query := `
		SELECT
			sc.id,
			sc.title,
			sc.description,
			sc.article_count,
			COUNT(DISTINCT pl.post_id) as total_shares,
			sc.last_updated_at
		FROM story_clusters sc
		LEFT JOIN story_articles sa ON sc.id = sa.story_id
		LEFT JOIN post_links pl ON sa.link_id = pl.link_id
		WHERE sc.is_active = true
		GROUP BY sc.id
		ORDER BY sc.last_updated_at DESC
		LIMIT 20
	`

	rows, err := db.Query(query)
	if err != nil {
		log.Printf("Failed to fetch stories: %v", err)
		return
	}
	defer rows.Close()

	storyNum := 1
	for rows.Next() {
		var id, articleCount, totalShares int
		var title, description, lastUpdated string

		if err := rows.Scan(&id, &title, &description, &articleCount, &totalShares, &lastUpdated); err != nil {
			log.Printf("Error scanning story: %v", err)
			continue
		}

		log.Printf("\n%d. %s", storyNum, title)
		log.Printf("   Story ID: %d | Articles: %d | Total shares: %d", id, articleCount, totalShares)
		if description != "" {
			log.Printf("   %s", truncate(description, 100))
		}

		// Fetch articles in this story
		articleQuery := `
			SELECT l.title, l.normalized_url, sa.similarity_score
			FROM story_articles sa
			JOIN links l ON sa.link_id = l.id
			WHERE sa.story_id = $1
			ORDER BY sa.similarity_score DESC
		`

		articleRows, err := db.Query(articleQuery, id)
		if err != nil {
			continue
		}

		articleNum := 1
		for articleRows.Next() {
			var articleTitle, url string
			var similarity float32
			if err := articleRows.Scan(&articleTitle, &url, &similarity); err != nil {
				continue
			}

			log.Printf("     %d) [%.2f] %s", articleNum, similarity, truncate(articleTitle, 60))
			log.Printf("        %s", url)
			articleNum++
		}
		articleRows.Close()

		storyNum++
	}

	if storyNum == 1 {
		log.Println("\nNo stories found. Run without --display-only to create stories.")
	}
}

// runDatabaseMigration runs the embeddings migration
func runDatabaseMigration(db *sql.DB) error {
	log.Println("Running migration 006_embeddings_and_stories.sql...")

	migration := `
-- Enable article embeddings
CREATE TABLE IF NOT EXISTS article_embeddings (
    link_id INTEGER PRIMARY KEY REFERENCES links(id) ON DELETE CASCADE,
    embedding_vector FLOAT4[],
    embedding_model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    full_text TEXT,
    byline TEXT,
    site_name TEXT,
    scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_embeddings_model ON article_embeddings(embedding_model);
CREATE INDEX IF NOT EXISTS idx_embeddings_scraped ON article_embeddings(scraped_at);

-- Story clusters
CREATE TABLE IF NOT EXISTS story_clusters (
    id SERIAL PRIMARY KEY,
    title TEXT,
    description TEXT,
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    article_count INTEGER DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS idx_story_clusters_updated ON story_clusters(last_updated_at);
CREATE INDEX IF NOT EXISTS idx_story_clusters_active ON story_clusters(is_active);

-- Story articles junction
CREATE TABLE IF NOT EXISTS story_articles (
    story_id INTEGER REFERENCES story_clusters(id) ON DELETE CASCADE,
    link_id INTEGER REFERENCES links(id) ON DELETE CASCADE,
    similarity_score FLOAT4,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (story_id, link_id)
);

CREATE INDEX IF NOT EXISTS idx_story_articles_story ON story_articles(story_id);
CREATE INDEX IF NOT EXISTS idx_story_articles_link ON story_articles(link_id);
CREATE INDEX IF NOT EXISTS idx_story_articles_score ON story_articles(similarity_score DESC);

-- Classification runs metadata
CREATE TABLE IF NOT EXISTS classification_runs (
    id SERIAL PRIMARY KEY,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    articles_processed INTEGER DEFAULT 0,
    stories_created INTEGER DEFAULT 0,
    similarity_threshold FLOAT4,
    embedding_model TEXT,
    notes TEXT
);

CREATE INDEX IF NOT EXISTS idx_classification_runs_started ON classification_runs(started_at);
`

	_, err := db.Exec(migration)
	return err
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
