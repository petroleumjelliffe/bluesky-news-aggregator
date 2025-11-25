-- Add author DID and degree to posts table for filtering by network degree

ALTER TABLE posts
ADD COLUMN IF NOT EXISTS author_did TEXT,
ADD COLUMN IF NOT EXISTS author_degree INTEGER;

-- Index for filtering by degree
CREATE INDEX IF NOT EXISTS idx_posts_author_degree ON posts(author_degree);

-- Index for looking up by DID
CREATE INDEX IF NOT EXISTS idx_posts_author_did ON posts(author_did);

-- Update trending_links view to support degree filtering
DROP VIEW IF EXISTS trending_links;

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
    ARRAY_AGG(DISTINCT p.author_handle) as sharers,
    ARRAY_AGG(DISTINCT p.author_degree) FILTER (WHERE p.author_degree IS NOT NULL) as degrees
FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
GROUP BY l.id;
