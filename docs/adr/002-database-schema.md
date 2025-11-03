# ADR 002: Database Schema Design

**Status**: Accepted

**Date**: 2025-11-02

## Decision

Use PostgreSQL with a normalized schema optimized for link aggregation and trending analysis.

## Schema

### Core Tables

**posts**
```sql
CREATE TABLE posts (
    id TEXT PRIMARY KEY,              -- Bluesky post URI
    author_handle TEXT NOT NULL,
    content TEXT,                     -- Post text
    created_at TIMESTAMP NOT NULL,
    indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_posts_created_at ON posts(created_at);
CREATE INDEX idx_posts_author ON posts(author_handle);
```

**links**
```sql
CREATE TABLE links (
    id SERIAL PRIMARY KEY,
    original_url TEXT NOT NULL,       -- URL as posted
    normalized_url TEXT NOT NULL UNIQUE, -- Canonicalized URL
    title TEXT,                       -- OpenGraph title
    description TEXT,                 -- OpenGraph description
    og_image_url TEXT,               -- OpenGraph image
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_fetched_at TIMESTAMP        -- Last OG metadata fetch
);

CREATE INDEX idx_links_normalized ON links(normalized_url);
```

**post_links** (junction table)
```sql
CREATE TABLE post_links (
    post_id TEXT REFERENCES posts(id) ON DELETE CASCADE,
    link_id INTEGER REFERENCES links(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, link_id)
);

CREATE INDEX idx_post_links_link ON post_links(link_id);
CREATE INDEX idx_post_links_post ON post_links(post_id);
```

**poll_state** (cursor tracking)
```sql
CREATE TABLE poll_state (
    user_handle TEXT PRIMARY KEY,
    last_cursor TEXT,                 -- Bluesky cursor (timestamp)
    last_polled_at TIMESTAMP,
    posts_fetched_count INTEGER DEFAULT 0
);
```

### Views

**trending_links**
```sql
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
```

## Design Decisions

### URL Normalization
- Store both original and normalized URLs
- Normalized URL is UNIQUE to prevent duplicates
- Handles URL shorteners (e.g., wapo.st → washingtonpost.com)
- Uses `goware/urlx` library for normalization

### Post-Link Relationship
- Many-to-many via junction table
- Single post can contain multiple links
- Same link can appear in multiple posts
- CASCADE delete maintains referential integrity

### No Users Table
- System follows authenticated user's follows list
- `GetFollows()` API provides dynamic list (342 accounts)
- No need to store user data separately
- Invalid/deleted accounts handled via permanent error detection

### Cursor Storage
- Bluesky cursors are timestamps (e.g., "2025-08-11T23:26:32.374Z")
- Stored per user_handle for pagination
- Empty cursor triggers initial 24-hour ingestion
- Valid cursor enables incremental polling

### Metadata Caching
- OpenGraph data stored in `links` table
- `last_fetched_at` tracks freshness
- Currently no re-fetch logic (fetch once, use forever)
- Future: TTL-based refresh for stale metadata

## Query Patterns

**Get trending links (last 24 hours)**:
```sql
SELECT * FROM trending_links
WHERE last_shared_at > NOW() - INTERVAL '24 hours'
ORDER BY share_count DESC
LIMIT 100;
```

**Get links shared by specific author**:
```sql
SELECT l.* FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
WHERE p.author_handle = 'example.bsky.social';
```

**Check if account needs initial ingestion**:
```sql
SELECT last_cursor FROM poll_state
WHERE user_handle = 'example.bsky.social';
-- NULL or empty string = needs initial ingestion
```

## Database Configuration

**Connection** (`config/config.yaml`):
```yaml
database:
  host: localhost
  port: 5432
  user: petroleumjelliffe
  password: ""  # Local trusted connection
  dbname: bluesky_news
  sslmode: disable
```

**Location**:
- PostgreSQL data directory: `/usr/local/var/postgres` (macOS Homebrew)
- Database: `bluesky_news`

## Performance Considerations

**Indexes**:
- `posts.created_at`: Time-range queries
- `posts.author_handle`: Author-specific queries
- `links.normalized_url`: Deduplication lookups
- `post_links` compound PKs: Efficient joins

**Current Scale** (as of 2025-11-02):
- 342 accounts monitored
- ~15-20 posts/minute during active hours
- ~50-150 new links per 15-minute cycle

**Future Optimizations**:
- Materialized view for `trending_links` (refresh every 5-15 min)
- Partitioning `posts` table by `created_at` (when > 1M rows)
- Index on `links.first_seen_at` for time-based queries

## Migration

Migration file: `migrations/001_initial.sql`

Apply with:
```bash
make migrate
# or
go run cmd/migrate/main.go
```

## Consequences

### Positive
- Simple, normalized schema
- Efficient trending link calculation
- Easy to query and understand
- Good performance at current scale

### Negative
- No users table (assumes single authenticated account)
- No metadata refresh strategy
- View performance may degrade at scale
- No post content full-text search indexes

## Related ADRs
- ADR 001: Polling Architecture (describes data flow)
