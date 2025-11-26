# News Story Classifier

A standalone news story classifier that uses embeddings to group related articles and track stories over time.

## Overview

The classifier uses OpenAI embeddings (text-embedding-3-small) to analyze article content and group similar articles into story clusters. It combines link metadata (title, description) with full article text scraped using Mozilla's Readability library.

## Features

- **Full Article Scraping**: Extracts main content using go-readability
- **Embedding Generation**: Creates vector representations using OpenAI's embedding API
- **Smart Clustering**: Groups articles by similarity with configurable threshold
- **Story Tracking**: Maintains story clusters with article relationships
- **Standalone Testing**: Test classification without integrating into the firehose

## Architecture

```
Links (Database)
    ↓
Content Scraper (Mozilla Readability)
    ↓
Embedding Generator (OpenAI)
    ↓
Classifier (Cosine Similarity)
    ↓
Story Clusters (Database)
```

## Database Schema

The classifier adds four new tables:

### `article_embeddings`
Stores embeddings and scraped content for each link.

```sql
- link_id: Reference to links table
- embedding_vector: Float array (1536 dimensions)
- full_text: Cached article text
- embedding_model: Model used (text-embedding-3-small)
- scraped_at: Timestamp
```

### `story_clusters`
Groups of related articles representing news stories.

```sql
- id: Story ID
- title: Representative title
- description: Story summary
- article_count: Number of articles
- is_active: Whether story is still being updated
- last_updated_at: Timestamp
```

### `story_articles`
Links articles to stories (many-to-many).

```sql
- story_id: Reference to story_clusters
- link_id: Reference to links
- similarity_score: Similarity to story centroid
```

### `classification_runs`
Tracks classification runs and parameters.

## Setup

### 1. Install Dependencies

The classifier requires:
- PostgreSQL database (already configured)
- OpenAI API key for embeddings

```bash
# Dependencies are already in go.mod
go mod download
```

### 2. Set Environment Variables

Create a `.env` file or export:

```bash
# Required
export OPENAI_API_KEY="sk-..."

# Database (if not already configured)
export DB_HOST=localhost
export DB_PORT=5432
export DB_USER=postgres
export DB_PASSWORD=your-password
export DB_NAME=bluesky_news
export DB_SSLMODE=disable
```

### 3. Run Database Migration

```bash
# Run migration automatically
go run ./cmd/classify -migrate

# Or run manually
psql $DATABASE_URL -f migrations/006_embeddings_and_stories.sql
```

## Usage

### Basic Classification

Classify the 20 most recent links with at least 2 shares:

```bash
go run ./cmd/classify
```

### Custom Parameters

```bash
# Classify 50 links with similarity threshold 0.85
go run ./cmd/classify -limit 50 -threshold 0.85

# Only include links with 5+ shares
go run ./cmd/classify -limit 30 -min-shares 5 -threshold 0.80

# Quiet mode (less verbose)
go run ./cmd/classify -verbose=false
```

### Display Existing Stories

View stories without running classification:

```bash
go run ./cmd/classify -display-only
```

## Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-limit` | 20 | Number of recent links to classify |
| `-threshold` | 0.80 | Similarity threshold (0-1) for grouping |
| `-min-shares` | 2 | Minimum shares required for a link |
| `-verbose` | true | Enable detailed logging |
| `-display-only` | false | Only display existing stories |
| `-migrate` | false | Run database migration first |

## How It Works

### 1. Content Scraping

For each link:
- Fetches HTML from the URL
- Extracts main content using Mozilla's Readability algorithm
- Parses metadata (title, author, published date)
- Caches full text in `article_embeddings` table

### 2. Embedding Generation

Creates vector representation by combining:
- Title (weighted 3x for importance)
- Description (weighted 2x)
- Full article text (truncated to ~2500 tokens)

Uses OpenAI's `text-embedding-3-small` model (1536 dimensions).

### 3. Clustering Algorithm

Greedy clustering approach:

```
For each unassigned article:
  1. Calculate similarity to existing story centroids
  2. If similarity >= threshold: Add to that story
  3. Else: Create new story cluster
  4. Update story centroid (running average)
```

Similarity is measured using cosine similarity (0-1 scale).

### 4. Storage

Stories are saved with:
- Representative title and description
- Article count and relationships
- Similarity scores for each article
- Timestamps for tracking updates

## Tuning Similarity Threshold

The threshold determines how strict clustering is:

| Threshold | Behavior | Use Case |
|-----------|----------|----------|
| 0.70-0.75 | Very loose | Broad topics, more articles per story |
| 0.80-0.85 | **Recommended** | Balanced grouping |
| 0.90-0.95 | Very strict | Only near-identical articles |

## Cost Considerations

OpenAI embedding costs (as of 2024):
- `text-embedding-3-small`: ~$0.02 per 1M tokens
- Average article: ~500-1000 tokens
- 100 articles: ~$0.001-0.002

Embeddings are cached, so re-running classification on the same links is free.

## Example Output

```
Fetching up to 20 recent links with at least 2 shares...
Found 18 links to classify
======================================================================

[1/18] Processing link ID 4523...
  Scraping content from https://example.com/article
  Generating embedding...
  ✓ Processed: New AI regulation proposal announced

[2/18] Processing link ID 4521...
  Using cached embedding
  ✓ Processed: AI regulation details emerge

...

Successfully processed 18 articles
Clustering with similarity threshold: 0.80

  Created new story: 'New AI regulation proposal announced' (1 articles)
  Grouped 'AI regulation details emerge' (similarity: 0.87)
  Added 'EU AI Act passes final vote' to existing story (similarity: 0.84)

Created 5 story clusters

======================================================================
📊 CLASSIFICATION RESULTS
======================================================================
Duration:           2m15s
Articles processed: 18
Stories created:    5
Similarity threshold: 0.80

======================================================================
📰 DISCOVERED STORIES
======================================================================

1. New AI regulation proposal announced
   Story ID: 1 | Articles: 3 | Total shares: 8
   Details about new AI safety regulations proposed by government

     1) [0.92] New AI regulation proposal announced
        https://example.com/article1
     2) [0.87] AI regulation details emerge
        https://example.com/article2
     3) [0.84] EU AI Act passes final vote
        https://example.com/article3
```

## Integration Roadmap

### Phase 1: Standalone Testing (Current)
- ✅ Scrape article content
- ✅ Generate embeddings
- ✅ Classify existing links
- ✅ Store story clusters

### Phase 2: Live Integration
- [ ] Add classifier to firehose pipeline
- [ ] Real-time story updates as new posts arrive
- [ ] Incremental clustering (add to existing stories)
- [ ] Story deduplication and merging

### Phase 3: API & Scoring
- [ ] Add stories endpoint to REST API
- [ ] Story-based ranking alongside link-based
- [ ] Story lifecycle management (mark as stale)
- [ ] Trending stories over time

### Phase 4: Enhancements
- [ ] Multi-language support
- [ ] Custom embeddings for news domain
- [ ] Story summarization
- [ ] Related story detection

## Troubleshooting

### "OPENAI_API_KEY environment variable is required"

Set your OpenAI API key:
```bash
export OPENAI_API_KEY="sk-..."
```

### "Failed to connect to database"

Check your database configuration:
```bash
# Test connection
psql -h $DB_HOST -U $DB_USER -d $DB_NAME

# Verify config
go run ./cmd/classify -migrate
```

### "No links found matching criteria"

Your database may not have enough links. Lower the threshold:
```bash
go run ./cmd/classify -min-shares 1 -limit 10
```

### Scraping failures

Some sites may block scraping. The classifier will skip those links and continue with others. Check logs for details.

## Files

- `internal/scraper/content.go` - Article content extraction
- `internal/embeddings/embeddings.go` - Embedding generation
- `internal/classifier/classifier.go` - Clustering algorithm
- `cmd/classify/main.go` - CLI tool
- `migrations/006_embeddings_and_stories.sql` - Database schema

## Performance

Benchmarks (single-threaded):
- Scraping: ~1-3 seconds per article
- Embedding generation: ~200ms per article (OpenAI API)
- Clustering: <10ms for 100 articles

Total time for 20 articles: ~1-2 minutes

## Future Optimizations

- **Parallel scraping**: Process multiple articles concurrently
- **Batch embeddings**: Send multiple texts in one API call
- **pgvector extension**: Use native vector operations in PostgreSQL
- **Materialized story views**: Pre-compute trending stories
- **Incremental updates**: Only process new links since last run

## Questions?

See the main project README or open an issue on GitHub.
