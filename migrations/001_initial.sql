-- 001_initial.sql

-- Posts table
CREATE TABLE IF NOT EXISTS posts (
    id TEXT PRIMARY KEY,
    author_handle TEXT NOT NULL,
    content TEXT,
    created_at TIMESTAMP NOT NULL,
    indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_posts_created_at ON posts(created_at);
CREATE INDEX IF NOT EXISTS idx_posts_author ON posts(author_handle);

-- Links table
CREATE TABLE IF NOT EXISTS links (
    id SERIAL PRIMARY KEY,
    original_url TEXT NOT NULL,
    normalized_url TEXT NOT NULL UNIQUE,
    title TEXT,
    description TEXT,
    og_image_url TEXT,
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_fetched_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_links_normalized ON links(normalized_url);

-- Post-Link junction table
CREATE TABLE IF NOT EXISTS post_links (
    post_id TEXT REFERENCES posts(id) ON DELETE CASCADE,
    link_id INTEGER REFERENCES links(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, link_id)
);

CREATE INDEX IF NOT EXISTS idx_post_links_link ON post_links(link_id);
CREATE INDEX IF NOT EXISTS idx_post_links_post ON post_links(post_id);

-- Poll state tracking
CREATE TABLE IF NOT EXISTS poll_state (
    user_handle TEXT PRIMARY KEY,
    last_cursor TEXT,
    last_polled_at TIMESTAMP,
    posts_fetched_count INTEGER DEFAULT 0
);

-- View for trending links (can be materialized for better performance)
CREATE OR REPLACE VIEW trending_links AS
SELECT 
    l.id,
    l.normalized_url,
    l.original_url,
    l.title,
    l.description,
    l.og_image_url,
    COUNT(DISTINCT pl.post_id) as share_count,
    MAX(p.created_at) as last_shared_at,
    ARRAY_AGG(DISTINCT p.author_handle) as sharers
FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
GROUP BY l.id;

-- Optional: Create materialized view for better performance
-- Refresh this periodically (every 5-15 minutes)
-- CREATE MATERIALIZED VIEW trending_links_materialized AS
-- SELECT * FROM trending_links;
-- CREATE INDEX idx_trending_share_count ON trending_links_materialized(share_count);
