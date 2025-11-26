-- 006_embeddings_and_stories.sql
-- News story classification with embeddings

-- Enable pgvector extension for efficient similarity search
-- Note: This requires pgvector to be installed: https://github.com/pgvector/pgvector
-- Installation: CREATE EXTENSION IF NOT EXISTS vector;
-- For now, we'll use array of floats and can migrate to vector type later

-- Article embeddings table
-- Stores embeddings generated from article content (title + description + full text)
CREATE TABLE IF NOT EXISTS article_embeddings (
    link_id INTEGER PRIMARY KEY REFERENCES links(id) ON DELETE CASCADE,
    embedding_vector FLOAT4[], -- Array of floats (will be 1536 dims for text-embedding-3-small)
    embedding_model TEXT NOT NULL DEFAULT 'text-embedding-3-small', -- Track which model generated this
    full_text TEXT, -- Cached full article text from scraping
    byline TEXT, -- Article author
    site_name TEXT, -- Source site name
    scraped_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_embeddings_model ON article_embeddings(embedding_model);
CREATE INDEX IF NOT EXISTS idx_embeddings_scraped ON article_embeddings(scraped_at);

-- Story clusters table
-- Groups related articles into news stories
CREATE TABLE IF NOT EXISTS story_clusters (
    id SERIAL PRIMARY KEY,
    title TEXT, -- Representative title for the story
    description TEXT, -- Summary of the story
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    article_count INTEGER DEFAULT 0, -- Number of articles in this story
    is_active BOOLEAN DEFAULT TRUE -- Whether story is still being updated
);

CREATE INDEX IF NOT EXISTS idx_story_clusters_updated ON story_clusters(last_updated_at);
CREATE INDEX IF NOT EXISTS idx_story_clusters_active ON story_clusters(is_active);

-- Story articles junction table
-- Links articles (links) to story clusters
CREATE TABLE IF NOT EXISTS story_articles (
    story_id INTEGER REFERENCES story_clusters(id) ON DELETE CASCADE,
    link_id INTEGER REFERENCES links(id) ON DELETE CASCADE,
    similarity_score FLOAT4, -- Similarity to story centroid or representative article
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (story_id, link_id)
);

CREATE INDEX IF NOT EXISTS idx_story_articles_story ON story_articles(story_id);
CREATE INDEX IF NOT EXISTS idx_story_articles_link ON story_articles(link_id);
CREATE INDEX IF NOT EXISTS idx_story_articles_score ON story_articles(similarity_score DESC);

-- Classification metadata
-- Tracks classification runs and parameters
CREATE TABLE IF NOT EXISTS classification_runs (
    id SERIAL PRIMARY KEY,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    articles_processed INTEGER DEFAULT 0,
    stories_created INTEGER DEFAULT 0,
    similarity_threshold FLOAT4, -- Threshold used for clustering
    embedding_model TEXT,
    notes TEXT
);

CREATE INDEX IF NOT EXISTS idx_classification_runs_started ON classification_runs(started_at);

-- View for story details with article counts and metadata
CREATE OR REPLACE VIEW story_details AS
SELECT
    sc.id,
    sc.title,
    sc.description,
    sc.first_seen_at,
    sc.last_updated_at,
    sc.article_count,
    sc.is_active,
    COUNT(DISTINCT pl.post_id) as total_shares,
    COUNT(DISTINCT sa.link_id) as linked_articles,
    ARRAY_AGG(DISTINCT l.normalized_url) as article_urls,
    MAX(p.created_at) as most_recent_share
FROM story_clusters sc
LEFT JOIN story_articles sa ON sc.id = sa.story_id
LEFT JOIN links l ON sa.link_id = l.id
LEFT JOIN post_links pl ON l.id = pl.link_id
LEFT JOIN posts p ON pl.post_id = p.id
GROUP BY sc.id;

-- Function to update story cluster article count
CREATE OR REPLACE FUNCTION update_story_article_count()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE story_clusters
        SET article_count = article_count + 1,
            last_updated_at = CURRENT_TIMESTAMP
        WHERE id = NEW.story_id;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE story_clusters
        SET article_count = article_count - 1,
            last_updated_at = CURRENT_TIMESTAMP
        WHERE id = OLD.story_id;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Trigger to automatically update article count
CREATE TRIGGER trigger_update_story_count
AFTER INSERT OR DELETE ON story_articles
FOR EACH ROW
EXECUTE FUNCTION update_story_article_count();
