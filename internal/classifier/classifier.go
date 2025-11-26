package classifier

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/lib/pq"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/embeddings"
	"github.com/petroleumjelliffe/bluesky-news-aggregator/internal/scraper"
)

// Classifier groups related articles into news stories using embeddings
type Classifier struct {
	db                 *sql.DB
	scraper            *scraper.Scraper
	embeddingService   *embeddings.EmbeddingService
	similarityThreshold float32 // Threshold for considering articles similar (0-1)
}

// NewClassifier creates a new article classifier
func NewClassifier(db *sql.DB, embeddingService *embeddings.EmbeddingService, similarityThreshold float32) *Classifier {
	return &Classifier{
		db:                 db,
		scraper:            scraper.NewScraper(),
		embeddingService:   embeddingService,
		similarityThreshold: similarityThreshold,
	}
}

// Article represents a link with its embedding
type Article struct {
	LinkID      int
	URL         string
	Title       string
	Description string
	FullText    string
	Embedding   []float32
}

// Story represents a cluster of related articles
type Story struct {
	ID          int
	Title       string
	Description string
	Articles    []Article
	Centroid    []float32 // Average embedding of all articles
}

// ClassifyLinks processes links and groups them into stories
func (c *Classifier) ClassifyLinks(linkIDs []int, verbose bool) (*ClassificationResult, error) {
	result := &ClassificationResult{
		StartedAt: time.Now(),
	}

	if verbose {
		log.Printf("Starting classification of %d links...", len(linkIDs))
	}

	// Step 1: Process each link - scrape content and generate embeddings
	articles := make([]Article, 0, len(linkIDs))
	for i, linkID := range linkIDs {
		if verbose {
			log.Printf("[%d/%d] Processing link ID %d...", i+1, len(linkIDs), linkID)
		}

		article, err := c.processLink(linkID, verbose)
		if err != nil {
			if verbose {
				log.Printf("  ⚠ Skipping link %d: %v", linkID, err)
			}
			continue
		}

		articles = append(articles, *article)
		result.ArticlesProcessed++

		if verbose {
			log.Printf("  ✓ Processed: %s", truncate(article.Title, 60))
		}
	}

	if len(articles) == 0 {
		return result, fmt.Errorf("no articles could be processed")
	}

	if verbose {
		log.Printf("\nSuccessfully processed %d articles", len(articles))
		log.Printf("Clustering with similarity threshold: %.2f\n", c.similarityThreshold)
	}

	// Step 2: Cluster articles into stories
	stories := c.clusterArticles(articles, verbose)
	result.StoriesCreated = len(stories)

	if verbose {
		log.Printf("\nCreated %d story clusters", len(stories))
	}

	// Step 3: Save stories to database
	for i, story := range stories {
		if verbose {
			log.Printf("\nStory %d: %s (%d articles)", i+1, truncate(story.Title, 60), len(story.Articles))
		}

		storyID, err := c.saveStory(story)
		if err != nil {
			if verbose {
				log.Printf("  ⚠ Failed to save story: %v", err)
			}
			continue
		}

		if verbose {
			log.Printf("  ✓ Saved as story ID %d", storyID)
		}
	}

	result.CompletedAt = time.Now()
	result.Duration = result.CompletedAt.Sub(result.StartedAt)

	// Save classification run metadata
	c.saveClassificationRun(result)

	return result, nil
}

// processLink scrapes content and generates embedding for a link
func (c *Classifier) processLink(linkID int, verbose bool) (*Article, error) {
	// Get link info from database
	var url, title, description string
	err := c.db.QueryRow(`
		SELECT normalized_url, COALESCE(title, ''), COALESCE(description, '')
		FROM links WHERE id = $1
	`, linkID).Scan(&url, &title, &description)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch link: %w", err)
	}

	// Check if we already have an embedding
	var existingEmbedding pq.Float32Array
	var existingText string
	err = c.db.QueryRow(`
		SELECT embedding_vector, COALESCE(full_text, '')
		FROM article_embeddings WHERE link_id = $1
	`, linkID).Scan(&existingEmbedding, &existingText)

	if err == nil && len(existingEmbedding) > 0 {
		// Already have embedding, reuse it
		if verbose {
			log.Printf("  Using cached embedding")
		}
		return &Article{
			LinkID:      linkID,
			URL:         url,
			Title:       title,
			Description: description,
			FullText:    existingText,
			Embedding:   []float32(existingEmbedding),
		}, nil
	}

	// Scrape article content
	if verbose {
		log.Printf("  Scraping content from %s", url)
	}

	content, err := c.scraper.ExtractArticleContent(url)
	if err != nil {
		return nil, fmt.Errorf("scraping failed: %w", err)
	}

	// Use scraped metadata if database doesn't have it
	if title == "" && content.Title != "" {
		title = content.Title
	}
	if description == "" && content.Excerpt != "" {
		description = content.Excerpt
	}

	// Generate embedding
	if verbose {
		log.Printf("  Generating embedding...")
	}

	embeddingInput := embeddings.ArticleInput{
		Title:       title,
		Description: description,
		FullText:    content.FullText,
		URL:         url,
	}

	embedding, err := c.embeddingService.GenerateArticleEmbedding(embeddingInput)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	// Store embedding in database
	_, err = c.db.Exec(`
		INSERT INTO article_embeddings (link_id, embedding_vector, full_text, byline, site_name, embedding_model)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (link_id) DO UPDATE SET
			embedding_vector = EXCLUDED.embedding_vector,
			full_text = EXCLUDED.full_text,
			updated_at = CURRENT_TIMESTAMP
	`, linkID, pq.Array(embedding), content.FullText, content.Byline, content.SiteName, "text-embedding-3-small")
	if err != nil {
		if verbose {
			log.Printf("  ⚠ Warning: Failed to cache embedding: %v", err)
		}
	}

	return &Article{
		LinkID:      linkID,
		URL:         url,
		Title:       title,
		Description: description,
		FullText:    content.FullText,
		Embedding:   embedding,
	}, nil
}

// clusterArticles groups articles into stories using similarity threshold
func (c *Classifier) clusterArticles(articles []Article, verbose bool) []Story {
	if len(articles) == 0 {
		return nil
	}

	var stories []Story
	assigned := make(map[int]bool) // Track which articles are assigned

	// Greedy clustering: for each unassigned article, create/join a cluster
	for i, article := range articles {
		if assigned[i] {
			continue
		}

		// Try to find an existing story this article belongs to
		var bestStory *Story
		var bestSimilarity float32 = 0

		for j := range stories {
			similarity := embeddings.CosineSimilarity(article.Embedding, stories[j].Centroid)
			if similarity > bestSimilarity && similarity >= c.similarityThreshold {
				bestSimilarity = similarity
				bestStory = &stories[j]
			}
		}

		if bestStory != nil {
			// Add to existing story
			bestStory.Articles = append(bestStory.Articles, article)
			bestStory.Centroid = c.updateCentroid(bestStory.Centroid, article.Embedding, len(bestStory.Articles))
			assigned[i] = true

			if verbose {
				log.Printf("  Added '%s' to existing story (similarity: %.3f)", truncate(article.Title, 40), bestSimilarity)
			}
		} else {
			// Create new story
			newStory := Story{
				Title:       article.Title,
				Description: article.Description,
				Articles:    []Article{article},
				Centroid:    article.Embedding,
			}

			// Check if any remaining articles belong to this new story
			for j := i + 1; j < len(articles); j++ {
				if assigned[j] {
					continue
				}

				similarity := embeddings.CosineSimilarity(articles[j].Embedding, newStory.Centroid)
				if similarity >= c.similarityThreshold {
					newStory.Articles = append(newStory.Articles, articles[j])
					newStory.Centroid = c.updateCentroid(newStory.Centroid, articles[j].Embedding, len(newStory.Articles))
					assigned[j] = true

					if verbose {
						log.Printf("  Grouped '%s' (similarity: %.3f)", truncate(articles[j].Title, 40), similarity)
					}
				}
			}

			stories = append(stories, newStory)
			assigned[i] = true

			if verbose {
				log.Printf("  Created new story: '%s' (%d articles)", truncate(newStory.Title, 40), len(newStory.Articles))
			}
		}
	}

	return stories
}

// updateCentroid calculates running average of embeddings
func (c *Classifier) updateCentroid(currentCentroid []float32, newEmbedding []float32, count int) []float32 {
	if len(currentCentroid) == 0 {
		return newEmbedding
	}

	centroid := make([]float32, len(currentCentroid))
	weight := 1.0 / float32(count)

	for i := range centroid {
		// Running average: new_avg = old_avg + (new_value - old_avg) / count
		centroid[i] = currentCentroid[i] + (newEmbedding[i]-currentCentroid[i])*weight
	}

	return centroid
}

// saveStory saves a story cluster to the database
func (c *Classifier) saveStory(story Story) (int, error) {
	// Insert story cluster
	var storyID int
	err := c.db.QueryRow(`
		INSERT INTO story_clusters (title, description, article_count)
		VALUES ($1, $2, $3)
		RETURNING id
	`, story.Title, story.Description, len(story.Articles)).Scan(&storyID)
	if err != nil {
		return 0, fmt.Errorf("failed to insert story: %w", err)
	}

	// Link articles to story
	for _, article := range story.Articles {
		similarity := embeddings.CosineSimilarity(article.Embedding, story.Centroid)
		_, err := c.db.Exec(`
			INSERT INTO story_articles (story_id, link_id, similarity_score)
			VALUES ($1, $2, $3)
			ON CONFLICT (story_id, link_id) DO UPDATE SET
				similarity_score = EXCLUDED.similarity_score
		`, storyID, article.LinkID, similarity)
		if err != nil {
			return 0, fmt.Errorf("failed to link article: %w", err)
		}
	}

	return storyID, nil
}

// saveClassificationRun saves metadata about the classification run
func (c *Classifier) saveClassificationRun(result *ClassificationResult) error {
	_, err := c.db.Exec(`
		INSERT INTO classification_runs (
			started_at, completed_at, articles_processed, stories_created,
			similarity_threshold, embedding_model
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, result.StartedAt, result.CompletedAt, result.ArticlesProcessed,
		result.StoriesCreated, c.similarityThreshold, "text-embedding-3-small")
	return err
}

// ClassificationResult holds the results of a classification run
type ClassificationResult struct {
	StartedAt         time.Time
	CompletedAt       time.Time
	Duration          time.Duration
	ArticlesProcessed int
	StoriesCreated    int
}

// truncate truncates a string to maxLen characters
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
