# Bluesky News Aggregator - Project Context

**Version**: 2.0.0
**Last Updated**: 2025-11-26
**Purpose**: Quick-start context document for AI assistants working on this project

---

## 1. Project Overview

### What It Is
A news aggregator that surfaces the most-shared links from your Bluesky network, similar to News.me and Nuzzle. It monitors posts from accounts you follow (and their follows) on Bluesky, extracts shared URLs, and ranks them by popularity.

### Current State
- **Network Size**: 49,831 monitored accounts (343 1st-degree + 49,488 2nd-degree)
- **Data Source**: Real-time Jetstream firehose + API backfill
- **Posts Tracked**: 160,000+ posts in database
- **Architecture**: Event-driven with shared processing pipeline
- **Deployment**: Render (API + Firehose worker + PostgreSQL)

### Key Features
- Real-time post ingestion via Jetstream WebSocket
- 2nd-degree network discovery (friends-of-friends)
- Degree-based filtering (1st, 2nd, or global views)
- OpenGraph metadata fetching (title, description, image)
- Configurable time windows (1-24 hours)
- Share count ranking
- Avatar support for sharers
- REST API with rate limiting and CORS

---

## 2. Quick Architecture Summary

### Component Overview
```
┌─────────────────────────────────────────────────────────────┐
│                     DATA SOURCES                              │
├──────────────────────┬────────────────────────────────────────┤
│  Jetstream Firehose  │        Bluesky API                     │
│  (Real-time events)  │  (Backfill, follows, profiles)         │
└──────────┬───────────┴──────────────┬─────────────────────────┘
           │                          │
           v                          v
    ┌──────────────┐          ┌──────────────┐
    │  cmd/firehose│          │ cmd/backfill │
    │              │          │ cmd/poller   │
    └──────┬───────┘          └──────┬───────┘
           │                          │
           └──────────┬───────────────┘
                      v
            ┌───────────────────┐
            │  internal/processor│ ◄─── DID Manager (degree tracking)
            │  (Shared Pipeline) │
            └─────────┬──────────┘
                      │
                      v
            ┌──────────────────┐
            │   PostgreSQL DB   │
            │  - posts          │
            │  - links          │
            │  - post_links     │
            │  - follows        │
            │  - network_accts  │
            └─────────┬─────────┘
                      │
                      v
              ┌───────────────┐
              │   cmd/api     │
              │  (REST API)   │
              └───────┬───────┘
                      │
                      v
              ┌───────────────┐
              │   Frontend    │
              │ (GitHub Pages)│
              └───────────────┘
```

### Data Flow

**Post Ingestion (Real-time)**:
1. Jetstream emits post creation event
2. `cmd/firehose` receives WebSocket message
3. DID Manager checks if author is in network (1st or 2nd degree)
4. `internal/processor` extracts URLs and metadata
5. Database stores post, links, and relationships
6. Metadata fetcher enriches links with OpenGraph data

**Post Ingestion (Backfill)**:
1. `cmd/backfill` or `cmd/poller` fetches posts via API
2. Same processing pipeline through `internal/processor`

**Trending Query**:
1. API receives `GET /api/trending?hours=24&degree=1`
2. `internal/aggregator` queries database
3. Ranks by share count (configurable strategy)
4. Returns links with sharers and avatars

### Key Architectural Decisions
- **ADR 005**: Jetstream firehose for real-time (replaces polling)
- **ADR 006**: Shared processing architecture (processor package)
- **ADR 009**: 2nd-degree network discovery with filtering
- **ADR 003**: Hybrid metadata fetching (96% from Bluesky, 4% scraped)
- **ADR 002**: Normalized URL storage with deduplication

---

## 3. Database Schema Quick Reference

### Tables

**posts**
```sql
id              VARCHAR(255) PRIMARY KEY  -- AT-URI (at://did/app.bsky.feed.post/rkey)
author_handle   VARCHAR(255) NOT NULL
author_did      VARCHAR(255) NOT NULL
author_degree   INTEGER DEFAULT 0         -- 1=1st-degree, 2=2nd-degree, 0=unknown
content         TEXT
created_at      TIMESTAMP NOT NULL
indexed_at      TIMESTAMP DEFAULT NOW()
```
Indexes: `idx_posts_created_at`, `idx_posts_author_did`, `idx_posts_author_degree`

**links**
```sql
id               SERIAL PRIMARY KEY
original_url     TEXT NOT NULL
normalized_url   TEXT NOT NULL UNIQUE     -- Dedupe key
title            TEXT
description      TEXT
og_image_url     TEXT
first_seen_at    TIMESTAMP DEFAULT NOW()
last_fetched_at  TIMESTAMP
```
Indexes: `idx_links_normalized_url`

**post_links** (many-to-many join table)
```sql
post_id  VARCHAR(255) REFERENCES posts(id) ON DELETE CASCADE
link_id  INTEGER REFERENCES links(id) ON DELETE CASCADE
PRIMARY KEY (post_id, link_id)
```
Indexes: `idx_post_links_link_id`, `idx_post_links_post_id`

**follows** (1st-degree network)
```sql
did                VARCHAR(255) PRIMARY KEY
handle             VARCHAR(255) NOT NULL UNIQUE
display_name       TEXT
avatar_url         TEXT
added_at           TIMESTAMP DEFAULT NOW()
last_seen_at       TIMESTAMP
backfill_completed BOOLEAN DEFAULT FALSE
```

**network_accounts** (1st + 2nd degree network)
```sql
did              VARCHAR(255) PRIMARY KEY
handle           VARCHAR(255) NOT NULL
display_name     TEXT
avatar_url       TEXT
degree           INTEGER NOT NULL          -- 1 or 2
source_count     INTEGER DEFAULT 1         -- How many 1st-degree follows follow this account
source_dids      TEXT                      -- JSON array of source DIDs
first_seen_at    TIMESTAMP DEFAULT NOW()
last_updated_at  TIMESTAMP DEFAULT NOW()
```
Indexes: `idx_network_accounts_degree`, `idx_network_accounts_source_count`

**jetstream_state** (cursor for firehose)
```sql
id                INTEGER PRIMARY KEY DEFAULT 1
cursor_time_us    BIGINT                -- Microsecond timestamp
last_updated_at   TIMESTAMP DEFAULT NOW()
```

**poll_state** (legacy cursor tracking, mostly deprecated)
```sql
handle      VARCHAR(255) PRIMARY KEY
cursor      TEXT
last_polled TIMESTAMP
```

### Key Relationships
- `posts.author_did` → `follows.did` or `network_accounts.did`
- `posts` ↔ `links` (many-to-many via `post_links`)
- `network_accounts` degree 2 sourced from `follows` (degree 1)

---

## 4. Complete Function Catalog

### CMD Packages (Binaries)

#### cmd/api
**Purpose**: HTTP REST API server

**Key Types**:
- `Server`: Main server with router, DB, aggregator
- `TrendingResponse`: JSON response with links
- `LinkResponse`: Single link with sharers and avatars

**Key Functions**:
- `main()`: Entry point, sets up server and routes
- `(s *Server) handleTrending(w, r)`: `GET /api/trending?hours=24&limit=50&degree=1&minSources=2`
- `(s *Server) handleHealth(w, r)`: `GET /health` endpoint
- `(s *Server) handleRoot(w, r)`: Serves static frontend
- `(s *Server) corsMiddleware(next)`: CORS handling
- `(s *Server) rateLimitMiddleware(next)`: IP-based rate limiting (100 req/min default)

**Location**: `cmd/api/main.go`

---

#### cmd/firehose
**Purpose**: Real-time Jetstream WebSocket consumer

**Key Functions**:
- `main()`: Connects to Jetstream, processes events via processor
- `formatBytes(bytes)`: Formats statistics output

**Flow**:
1. Loads DID manager from DB (network accounts)
2. Connects to `jetstream2.us-east.bsky.network`
3. Receives `app.bsky.feed.post` creation events
4. Filters by DID (1st or 2nd degree)
5. Passes to `processor.ProcessEvent()`
6. Updates cursor every 10 seconds

**Location**: `cmd/firehose/main.go`

---

#### cmd/backfill
**Purpose**: Historical data ingestion from Bluesky API

**Key Types**:
- `Backfiller`: Orchestrates backfill with rate limiting

**Key Methods**:
- `(b *Backfiller) backfillAccounts(follows)`: Concurrent backfill of all accounts
- `(b *Backfiller) backfillAccount(follow)`: Backfills single account (24 hours)
- `(b *Backfiller) fetchWithRetry()`: Exponential backoff retry logic
- `(b *Backfiller) processPost(post, did)`: Processes via shared processor

**Usage**:
```bash
go run cmd/backfill/main.go  # Backfills all accounts without backfill_completed=true
```

**Location**: `cmd/backfill/main.go`

---

#### cmd/poller
**Purpose**: Periodic polling of Bluesky feeds (legacy, mostly replaced by firehose)

**Key Types**:
- `Poller`: Manages polling state and concurrency

**Key Methods**:
- `(p *Poller) Poll()`: Polls all followed accounts
- `(p *Poller) pollAccount(handle)`: Polls single account (initial or regular)
- `(p *Poller) pollAccountInitial(handle)`: 24-hour initial ingestion
- `(p *Poller) pollAccountRegular(handle, cursor)`: Cursor-based incremental polling
- `(p *Poller) processPost(post)`: Extracts URLs, fetches metadata
- `(p *Poller) processExternalWithMetadata()`: Uses Bluesky's embed metadata (preferred)
- `(p *Poller) fetchOGDataAsync()`: Background scraper fallback

**Location**: `cmd/poller/main.go`

---

#### cmd/crawl-network
**Purpose**: Discovers 2nd-degree network (friends-of-friends)

**Key Functions**:
- `main()`: Crawls network with configurable degree and source count
- `printStats(db)`: Displays network statistics

**Flow**:
1. Sync 1st-degree follows from Bluesky API
2. For each 1st-degree account, fetch their follows
3. Count how many 1st-degree accounts follow each 2nd-degree candidate
4. Filter by `minSourceCount` (e.g., followed by 2+ friends)
5. Store in `network_accounts` table

**Usage**:
```bash
go run cmd/crawl-network/main.go  # Defaults: degree=2, minSources=2
```

**Location**: `cmd/crawl-network/main.go`

---

#### cmd/metadata-fetcher
**Purpose**: Batch fetches missing OpenGraph metadata

**Key Types**:
- `Config`: Concurrency, rate limiting, dry-run settings

**Key Functions**:
- `main()`: Fetches metadata for links where `title IS NULL`
- `getLinksNeedingMetadata(db)`: Queries links without metadata

**Usage**:
```bash
go run cmd/metadata-fetcher/main.go  # Scrapes missing metadata
```

**Location**: `cmd/metadata-fetcher/main.go`

---

#### cmd/janitor
**Purpose**: Database cleanup and maintenance

**Key Types**:
- `JanitorConfig`: Retention periods, dry-run mode

**Key Functions**:
- `main()`: Runs cleanup tasks
- `cleanupOldPosts(db, cfg)`: Deletes posts older than N days
- `cleanupOrphanedLinks(db, cfg)`: Removes links with no post_links
- `cleanupOldLinks(db, cfg)`: Removes links not shared recently (except trending)

**Usage**:
```bash
go run cmd/janitor/main.go  # Cleans up old data
```

**Location**: `cmd/janitor/main.go`

---

#### cmd/migrate
**Purpose**: Database migration runner

**Key Functions**:
- `main()`: Applies SQL migrations from `migrations/` directory

**Usage**:
```bash
go run cmd/migrate/main.go  # Runs migrations/*.sql in order
```

**Location**: `cmd/migrate/main.go`

---

#### cmd/migrate-follows
**Purpose**: One-time migration from poll_state to follows table

**Key Functions**:
- `main()`: Resolves handles to DIDs and migrates

**Location**: `cmd/migrate-follows/main.go` (likely deprecated)

---

### INTERNAL Packages (Library Code)

#### internal/processor
**Purpose**: SINGLE shared processing pipeline for all post sources (Jetstream, API, backfill)

**Key Types**:
```go
type Processor struct {
    db         *database.DB
    didManager DIDManager
    scraper    *scraper.Scraper
}

type DIDManager interface {
    GetDegree(did string) int  // Returns 1, 2, or 0
}

type PostRecord struct {
    Type      string    `json:"$type"`
    Text      string    `json:"text"`
    CreatedAt time.Time `json:"createdAt"`
    Embed     *Embed    `json:"embed,omitempty"`
}
```

**Key Functions**:
- `NewProcessor(db, didManager) *Processor`: Creates processor
- `(p *Processor) ProcessEvent(event *models.Event) error`: Processes Jetstream event

**Flow**:
1. Extracts URLs from post text and embeds
2. Normalizes URLs (removes tracking params)
3. Gets or creates link in DB (atomic upsert)
4. Uses Bluesky embed metadata if available (96% of cases)
5. Falls back to OpenGraph scraping if needed (4%)
6. Links post to link via `post_links` table
7. Fetches metadata asynchronously if missing

**When to Use**: Any time you're processing posts, use this shared pipeline to ensure consistency.

**Location**: `internal/processor/processor.go`

---

#### internal/database
**Purpose**: Database operations and models

**Key Types**: (See Section 3 for full schema)
- `DB`: sqlx wrapper
- `Post`, `Link`, `PostLink`, `Follow`, `NetworkAccount`
- `TrendingLink`: Aggregated link with share count and sharers
- `SharerAvatar`: Profile info for users who shared a link

**Key Functions**:

*Post Operations*:
- `(db *DB) InsertPost(post *Post) error`: Inserts post (idempotent)

*Link Operations*:
- `(db *DB) GetOrCreateLink(originalURL, normalizedURL) (*Link, error)`: Atomic upsert, handles race conditions
- `(db *DB) UpdateLinkMetadata(linkID, title, description, imageURL) error`: Updates OpenGraph data
- `(db *DB) MarkLinkFetched(linkID) error`: Sets `last_fetched_at`
- `(db *DB) LinkPostToLink(postID, linkID) error`: Creates post-link relationship

*Trending/Aggregation*:
- `(db *DB) GetTrendingLinks(hoursBack, limit) ([]TrendingLink, error)`: Global trending
- `(db *DB) GetTrendingLinksByDegree(hoursBack, limit, degree) ([]TrendingLink, error)`: Filtered by degree
- `(db *DB) GetLinkSharers(linkID) ([]SharerAvatar, error)`: Gets profiles of sharers

*Follow Management*:
- `(db *DB) GetAllFollows() ([]Follow, error)`: Returns 1st-degree follows
- `(db *DB) AddFollow(did, handle, displayName, avatarURL) error`: Adds follow
- `(db *DB) RemoveFollow(did) error`: Removes follow
- `(db *DB) UpdateFollowLastSeen(did) error`: Updates timestamp
- `(db *DB) MarkBackfillCompleted(did) error`: Marks backfill done

*Network Management*:
- `(db *DB) UpsertNetworkAccount(did, handle, displayName, avatarURL, degree, sourceCount, sourceDIDs) error`: Adds/updates network account
- `(db *DB) GetNetworkAccountsByDegree(degree, minSourceCount) ([]NetworkAccount, error)`: Gets accounts by degree
- `(db *DB) GetAllNetworkDIDs() (map[string]int, error)`: Returns `map[did]->degree`
- `(db *DB) GetNetworkStats() (map[string]interface{}, error)`: Network statistics

*Jetstream State*:
- `(db *DB) GetJetstreamCursor() (*int64, error)`: Gets cursor
- `(db *DB) UpdateJetstreamCursor(cursorTimeUS) error`: Updates cursor

*Cleanup*:
- `(db *DB) DeleteOldPosts(cutoff time.Time) (int, error)`: Deletes old posts
- `(db *DB) DeleteOrphanedPostLinks() (int, error)`: Removes orphaned post_links
- `(db *DB) DeleteUnsharedLinks(cutoff, trendingThreshold) (int, error)`: Removes unshared links

**Location**: `internal/database/db.go`

---

#### internal/bluesky
**Purpose**: Bluesky AT Protocol API client

**Key Types**:
```go
type Client struct { ... }  // Authenticated client with JWT token

type Post struct {
    URI       string    // at://did/app.bsky.feed.post/rkey
    CID       string
    Author    Author
    Record    Record
    Embed     *Embed
    IndexedAt time.Time
}

type Author struct {
    DID         string
    Handle      string
    DisplayName string
    Avatar      string
}

type FeedResponse struct {
    Feed   []FeedItem
    Cursor string  // For pagination
}

type FollowsResponse struct {
    Follows []Follow
    Cursor  string
}

type Embed struct {
    Type     string          // "app.bsky.embed.external#view"
    External *EmbedExternal
    Record   *EmbedRecord    // For quote posts
}

type EmbedExternal struct {
    URI         string  // The shared URL
    Title       string
    Description string
    Thumb       string  // Image URL
}
```

**Key Functions**:
- `NewClient(handle, password) (*Client, error)`: Creates authenticated client (gets JWT)
- `(c *Client) GetDID() string`: Returns authenticated user's DID
- `(c *Client) GetAuthorFeed(handle, cursor, limit) (*FeedResponse, error)`: Fetches author's posts
- `(c *Client) GetFollows(handle) ([]string, error)`: Gets follow handles only
- `(c *Client) GetFollowsWithMetadata(handle) ([]Follow, error)`: Gets follows with profiles

**API Details**:
- Base URL: `https://bsky.social/xrpc`
- Authentication: JWT token via `com.atproto.server.createSession`
- Feed: `app.bsky.feed.getAuthorFeed`
- Follows: `app.bsky.graph.getFollows`

**Location**: `internal/bluesky/client.go`, `internal/bluesky/types.go`

---

#### internal/scraper
**Purpose**: Web scraping for OpenGraph metadata

**Key Types**:
```go
type OGData struct {
    Title       string
    Description string
    ImageURL    string
}

type Scraper struct { ... }  // HTTP/2 and HTTP/1.1 clients

type DomainRateLimiter struct { ... }  // Per-domain rate limiting
```

**Key Functions**:
- `NewScraper() *Scraper`: Creates scraper with dual HTTP clients
- `(s *Scraper) FetchOGData(urlStr) (*OGData, error)`: Fetches metadata with retry
- `NewDomainRateLimiter(minDelay) *DomainRateLimiter`: Creates per-domain limiter
- `(d *DomainRateLimiter) Wait(domain)`: Blocks until rate limit allows

**Features**:
- Tries HTTP/2 first, falls back to HTTP/1.1
- Exponential backoff (3 retries)
- Per-domain rate limiting (100ms default)
- Parses OpenGraph meta tags: `og:title`, `og:description`, `og:image`
- Falls back to `<title>` and meta description

**Location**: `internal/scraper/scraper.go`

---

#### internal/aggregator
**Purpose**: Link aggregation and ranking strategies

**Key Types**:
```go
type RankingStrategy interface {
    Rank(links []database.TrendingLink) []database.TrendingLink
}

type ShareCountRanking struct{}      // Simple share count (current)
type RecencyWeightedRanking struct{} // TODO: Recent shares weighted higher
type VelocityRanking struct{}        // TODO: Rate of sharing

type Aggregator struct {
    db     *database.DB
    ranker RankingStrategy
}
```

**Key Functions**:
- `NewAggregator(db, ranker) *Aggregator`: Creates aggregator
- `(a *Aggregator) GetTrendingLinks(hoursBack, limit) ([]TrendingLink, error)`: Global trending
- `(a *Aggregator) GetTrendingLinksByDegree(hoursBack, limit, degree) ([]TrendingLink, error)`: Degree-filtered

**Ranking Strategies**:
- `ShareCountRanking`: Ranks by total share count (descending)
- `RecencyWeightedRanking`: (TODO) Recent shares weighted higher
- `VelocityRanking`: (TODO) Ranks by sharing velocity

**Location**: `internal/aggregator/aggregator.go`

---

#### internal/didmanager
**Purpose**: Tracks followed DIDs for filtering (1st & 2nd degree network)

**Key Types**:
```go
type Manager struct {
    didMap   map[string]int  // map[did]->degree (1 or 2)
    config   *Config
    mu       sync.RWMutex
}

type Config struct {
    Include2ndDegree bool  // Whether to include 2nd-degree accounts
    MinSourceCount   int   // Minimum sources for 2nd-degree (e.g., 2)
}
```

**Key Functions**:
- `NewManager(db) *Manager`: Creates manager with defaults (1st+2nd degree, minSources=2)
- `NewManagerWithConfig(db, config) *Manager`: Creates with custom config
- `(m *Manager) LoadFromDatabase() error`: Loads DIDs from `network_accounts` table
- `(m *Manager) IsFollowed(did) bool`: Checks if DID is in network (any degree)
- `(m *Manager) GetDegree(did) int`: Returns degree (1, 2, or 0 if not found)
- `(m *Manager) GetDIDs() []string`: Returns all followed DIDs
- `(m *Manager) GetDIDsByDegree(degree) []string`: Returns DIDs by degree
- `(m *Manager) AddDID(did, degree)`: Adds DID to in-memory map
- `(m *Manager) RemoveDID(did)`: Removes DID
- `(m *Manager) Count() int`: Total DID count
- `(m *Manager) CountByDegree() map[int]int`: Counts by degree
- `(m *Manager) SetInclude2ndDegree(include)`: Enable/disable 2nd-degree
- `(m *Manager) IsIncluding2ndDegree() bool`: Returns if 2nd-degree enabled

**Usage**: Used by `cmd/firehose` to filter Jetstream events to only tracked DIDs.

**Location**: `internal/didmanager/manager.go`

---

#### internal/crawler
**Purpose**: Network crawler for 2nd-degree discovery

**Key Types**:
```go
type Crawler struct {
    db          *database.DB
    bskyClient  *bluesky.Client
    myDID       string
    rateLimiter *RateLimiter
    config      *Config
}

type Config struct {
    RequestsPerSecond int  // API rate limit (default: 5)
    SourceCountMin    int  // Min sources for 2nd-degree (default: 2)
}

type Candidate struct {
    DID         string
    Handle      string
    DisplayName string
    AvatarURL   string
    SourceCount int       // How many 1st-degree follows follow this account
    SourceDIDs  []string  // Which 1st-degree accounts follow this
}

type RateLimiter struct { ... }  // Token bucket rate limiter
```

**Key Functions**:
- `NewCrawler(db, bskyClient, myDID, config) *Crawler`: Creates crawler
- `(c *Crawler) SyncFirstDegree(ctx, myHandle) error`: Syncs 1st-degree follows from API
- `(c *Crawler) CrawlSecondDegree(ctx, sourceCountMin) error`: Crawls 2nd-degree network
- `(c *Crawler) GetStats() (map[string]interface{}, error)`: Gets network statistics
- `(c *Crawler) Close()`: Cleanup
- `NewRateLimiter(rps) *RateLimiter`: Creates token bucket rate limiter
- `(rl *RateLimiter) Wait(ctx) error`: Blocks until token available

**Workflow**:
1. Fetch all 1st-degree follows
2. For each 1st-degree account, fetch their follows (2nd-degree candidates)
3. Count how many 1st-degree accounts follow each 2nd-degree candidate
4. Filter by `minSourceCount` (e.g., ≥2 mutual friends)
5. Store in `network_accounts` table

**Location**: `internal/crawler/crawler.go`, `internal/crawler/ratelimit.go`

---

#### internal/jetstream
**Purpose**: Jetstream WebSocket client wrapper

**Key Types**:
```go
type Client struct { ... }

type EventHandler func(ctx context.Context, event *models.Event) error

type Config struct {
    WebsocketURL      string    // e.g., "jetstream2.us-east.bsky.network"
    Compress          bool
    WantedCollections []string  // e.g., ["app.bsky.feed.post"]
    WantedDIDs        []string  // Filter by DIDs (optional)
}
```

**Key Functions**:
- `NewClient(cfg, handler) (*Client, error)`: Creates Jetstream client
- `(c *Client) Connect(ctx, cursor) error`: Connects and reads events
- `(c *Client) Stats() (bytesRead, eventsRead int64)`: Returns statistics

**Event Format**:
```json
{
  "did": "did:plc:...",
  "time_us": 1234567890,
  "kind": "commit",
  "commit": {
    "collection": "app.bsky.feed.post",
    "operation": "create",
    "rkey": "...",
    "record": { ... }
  }
}
```

**Location**: `internal/jetstream/client.go`

---

#### internal/maintenance
**Purpose**: Database cleanup and maintenance

**Key Types**:
```go
type Config struct {
    RetentionHours       int  // How long to keep posts (default: 168 = 7 days)
    TrendingThreshold    int  // Min shares to keep link (default: 2)
    CleanupIntervalMin   int  // How often to run (default: 60)
    CursorUpdateInterval int  // How often to update cursor (default: 10s)
}
```

**Key Functions**:
- `StartupCleanup(db, config) error`: Runs cleanup on startup
- `PeriodicCleanup(db, config) error`: Runs single cleanup cycle
- `StartCleanupTicker(db, config)`: Starts background cleanup goroutine

**Cleanup Tasks**:
1. Delete posts older than `RetentionHours`
2. Delete orphaned `post_links` (posts deleted but links remain)
3. Delete links not shared recently AND share_count < `TrendingThreshold`

**Location**: `internal/maintenance/cleanup.go`

---

#### internal/urlutil
**Purpose**: URL normalization and extraction utilities

**Key Functions**:
- `ExtractURLs(text) []string`: Finds all URLs in text using regex
- `Normalize(rawURL) (string, error)`: Normalizes URL for deduplication

**Normalization Steps**:
1. Parse URL
2. Convert scheme to lowercase (`HTTP` → `http`)
3. Convert domain to lowercase (`Example.COM` → `example.com`)
4. Remove default ports (`:80`, `:443`)
5. Remove trailing slashes
6. Sort query parameters
7. Remove tracking parameters (`utm_*`, `fbclid`, etc.)
8. Remove fragment (`#section`)

**Example**:
```
https://Example.com:443/page?utm_source=twitter&id=123#top
→ https://example.com/page?id=123
```

**Location**: `internal/urlutil/normalize.go`

---

#### internal/config
**Purpose**: Centralized configuration with environment variable support

**Key Types**:
```go
type Config struct {
    Database DatabaseConfig
    Bluesky  BlueskyConfig
    Server   ServerConfig
    Polling  PollingConfig
    Cleanup  CleanupConfig
}

type DatabaseConfig struct {
    Host     string  // DB_HOST or localhost
    Port     int     // DB_PORT or 5432
    User     string  // DB_USER or postgres
    Password string  // DB_PASSWORD
    DBName   string  // DB_NAME or bluesky_news
    SSLMode  string  // DB_SSLMODE or disable
}

type BlueskyConfig struct {
    Handle   string  // BLUESKY_HANDLE
    Password string  // BLUESKY_PASSWORD (app password)
}

type ServerConfig struct {
    Host            string  // SERVER_HOST or 0.0.0.0
    Port            int     // SERVER_PORT or 8080
    TLSCertFile     string  // TLS_CERT_FILE
    TLSKeyFile      string  // TLS_KEY_FILE
    CORSAllowOrigin string  // CORS_ALLOW_ORIGIN
    RateLimitRPM    int     // RATE_LIMIT_RPM or 100
}

type PollingConfig struct {
    IntervalMinutes      int  // POLLING_INTERVAL_MINUTES or 15
    PostsPerPage         int  // POSTS_PER_PAGE or 50
    MaxConcurrent        int  // MAX_CONCURRENT or 10
    RateLimitMs          int  // RATE_LIMIT_MS or 100
    InitialLookbackHours int  // INITIAL_LOOKBACK_HOURS or 24
    MaxRetries           int  // MAX_RETRIES or 3
    RetryBackoffMs       int  // RETRY_BACKOFF_MS or 1000
    MaxPagesPerUser      int  // MAX_PAGES_PER_USER or 10
}

type CleanupConfig struct {
    RetentionHours      int  // CLEANUP_RETENTION_HOURS or 168 (7 days)
    CleanupIntervalMin  int  // CLEANUP_INTERVAL_MIN or 60
    TrendingThreshold   int  // TRENDING_THRESHOLD or 2
    CursorUpdateSeconds int  // CURSOR_UPDATE_SECONDS or 10
}
```

**Key Functions**:
- `Load() (*Config, error)`: Loads from `config/config.yaml` and environment variables (env takes precedence)
- `(c *DatabaseConfig) DatabaseConnString() string`: Returns connection string
- `(c *DatabaseConfig) DatabaseConnStringSafe() string`: Redacts password for logging
- `(c *ServerConfig) IsTLSEnabled() bool`: Checks if TLS is configured

**Configuration Priority**:
1. Environment variables (highest)
2. `config/config.yaml`
3. Defaults (lowest)

**Location**: `internal/config/config.go`

---

## 5. Command Binaries Reference

### When to Use Each Command

| Command | Purpose | When to Use |
|---------|---------|-------------|
| `cmd/api` | REST API server | Always running in production |
| `cmd/firehose` | Real-time Jetstream consumer | Always running in production (primary ingestion) |
| `cmd/poller` | Periodic API polling | Legacy/backup (mostly replaced by firehose) |
| `cmd/backfill` | Historical data ingestion | One-time or after adding new follows |
| `cmd/crawl-network` | 2nd-degree network discovery | Periodically (e.g., weekly) or when network grows |
| `cmd/metadata-fetcher` | Batch metadata fetching | One-time or cron job to fill gaps |
| `cmd/janitor` | Database cleanup | Cron job (daily) or manually |
| `cmd/migrate` | Database migrations | On deployment, after schema changes |

### Production Architecture (Render)

```
┌──────────────────┐
│   API Service    │  (cmd/api)
│   Port: 8080     │
└──────────────────┘
         │
         v
┌──────────────────┐
│  PostgreSQL DB   │
│   (Render DB)    │
└──────────────────┘
         ^
         │
┌──────────────────┐
│ Firehose Worker  │  (cmd/firehose)
│  (Background)    │
└──────────────────┘
         ^
         │
┌──────────────────┐
│ Cleanup Cron Job │  (cmd/janitor)
│   (Daily 2am)    │
└──────────────────┘
```

---

## 6. API Endpoints

### GET /api/trending

**Purpose**: Get trending links by share count

**Query Parameters**:
- `hours` (int, default: 24): Time window in hours (1-24)
- `limit` (int, default: 50): Max results (1-100)
- `degree` (int, optional): Filter by network degree (1, 2, or omit for all)
- `minSources` (int, optional): For degree=2, minimum mutual friends (default: 2)

**Example Requests**:
```bash
# Last 24 hours, all degrees
GET /api/trending?hours=24&limit=50

# Last 6 hours, 1st-degree only
GET /api/trending?hours=6&degree=1

# Last 12 hours, 2nd-degree with 3+ mutual friends
GET /api/trending?hours=12&degree=2&minSources=3
```

**Response**:
```json
{
  "links": [
    {
      "id": 123,
      "url": "https://example.com/article",
      "title": "Article Title",
      "description": "Article description from OpenGraph",
      "image_url": "https://example.com/og-image.jpg",
      "share_count": 15,
      "last_shared_at": "2025-11-26T10:30:00Z",
      "sharers": ["alice.bsky.social", "bob.bsky.social"],
      "sharer_avatars": [
        {
          "handle": "alice.bsky.social",
          "display_name": "Alice",
          "avatar_url": "https://cdn.bsky.app/avatar.jpg",
          "did": "did:plc:..."
        }
      ]
    }
  ]
}
```

**Rate Limiting**: 100 requests/minute per IP (configurable via `RATE_LIMIT_RPM`)

---

### GET /health

**Purpose**: Health check endpoint

**Response**:
```json
{
  "status": "healthy",
  "database": "connected"
}
```

---

### GET /

**Purpose**: Serves static frontend (index.html)

**CORS**: Configurable via `CORS_ALLOW_ORIGIN` environment variable

---

## 7. Configuration Guide

### Environment Variables

**Database**:
```bash
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=your-password
DB_NAME=bluesky_news
DB_SSLMODE=disable  # Use 'require' in production
```

**Bluesky**:
```bash
BLUESKY_HANDLE=your.handle.bsky.social
BLUESKY_PASSWORD=xxxx-xxxx-xxxx-xxxx  # App password from https://bsky.app/settings/app-passwords
```

**Server**:
```bash
SERVER_HOST=0.0.0.0
SERVER_PORT=8080
CORS_ALLOW_ORIGIN=https://your-domain.com
RATE_LIMIT_RPM=100
TLS_CERT_FILE=/path/to/cert.pem  # Optional
TLS_KEY_FILE=/path/to/key.pem    # Optional
```

**Polling** (mostly legacy):
```bash
POLLING_INTERVAL_MINUTES=15
POSTS_PER_PAGE=50
MAX_CONCURRENT=10
RATE_LIMIT_MS=100
INITIAL_LOOKBACK_HOURS=24
MAX_RETRIES=3
RETRY_BACKOFF_MS=1000
MAX_PAGES_PER_USER=10
```

**Cleanup**:
```bash
CLEANUP_RETENTION_HOURS=168      # 7 days
CLEANUP_INTERVAL_MIN=60          # Run every hour
TRENDING_THRESHOLD=2             # Keep links with 2+ shares
CURSOR_UPDATE_SECONDS=10         # Update Jetstream cursor every 10s
```

### Config File (config/config.yaml)

```yaml
database:
  host: localhost
  port: 5432
  user: postgres
  password: yourpassword
  dbname: bluesky_news
  sslmode: disable

bluesky:
  handle: your.handle.bsky.social
  password: xxxx-xxxx-xxxx-xxxx

server:
  host: 0.0.0.0
  port: 8080
  cors_allow_origin: https://your-domain.com
  rate_limit_rpm: 100

polling:
  interval_minutes: 15
  posts_per_page: 50
  max_concurrent: 10
  rate_limit_ms: 100
  initial_lookback_hours: 24
  max_retries: 3
  retry_backoff_ms: 1000
  max_pages_per_user: 10

cleanup:
  retention_hours: 168
  cleanup_interval_min: 60
  trending_threshold: 2
  cursor_update_seconds: 10
```

**Note**: Environment variables take precedence over config file values.

---

## 8. Common Workflows

### Workflow 1: Real-time Post Ingestion (Jetstream)

```
1. Jetstream emits event:
   {
     "did": "did:plc:abc123",
     "commit": {
       "collection": "app.bsky.feed.post",
       "operation": "create",
       "record": { "text": "Check this out https://example.com", ... }
     }
   }

2. cmd/firehose receives event
   ↓
3. DID Manager checks: IsFollowed("did:plc:abc123")
   - If not followed (degree=0), skip
   - If followed, get degree (1 or 2)
   ↓
4. processor.ProcessEvent(event)
   ↓
5. Extract URLs from text: ["https://example.com"]
   ↓
6. For each URL:
   a. Normalize: https://example.com
   b. GetOrCreateLink() → linkID=123 (atomic upsert)
   c. Check if embed has metadata (Bluesky's OpenGraph data)
      - If yes: UpdateLinkMetadata() immediately (96% of cases)
      - If no: Queue for scraping later (4% of cases)
   d. LinkPostToLink(postID, linkID)
   ↓
7. Background: scraper.FetchOGData() if needed
   ↓
8. Every 10 seconds: UpdateJetstreamCursor()
```

**Key Files**:
- `cmd/firehose/main.go`: Event receiver
- `internal/processor/processor.go`: Processing logic
- `internal/didmanager/manager.go`: Degree filtering
- `internal/database/db.go`: Database operations

---

### Workflow 2: Backfill Historical Data

```
1. User runs: go run cmd/backfill/main.go
   ↓
2. Load all follows where backfill_completed=false
   ↓
3. For each account (concurrent):
   a. Fetch last 24 hours via GetAuthorFeed()
   b. For each post:
      - Extract URLs
      - Normalize
      - GetOrCreateLink()
      - Use Bluesky embed metadata if available
      - LinkPostToLink()
      - Queue scraping if needed
   c. MarkBackfillCompleted()
   ↓
4. Background: metadata-fetcher scrapes missing data
```

**Key Files**:
- `cmd/backfill/main.go`: Backfill orchestration
- `internal/bluesky/client.go`: API calls
- `internal/processor/processor.go`: Shared processing

---

### Workflow 3: Trending Query

```
1. User requests: GET /api/trending?hours=24&degree=1&limit=50
   ↓
2. API handler parses params
   ↓
3. aggregator.GetTrendingLinksByDegree(24, 50, 1)
   ↓
4. Database query:
   SELECT l.*, COUNT(*) as share_count, array_agg(p.author_handle) as sharers
   FROM links l
   JOIN post_links pl ON l.id = pl.link_id
   JOIN posts p ON pl.post_id = p.id
   WHERE p.created_at > NOW() - INTERVAL '24 hours'
     AND p.author_degree = 1  -- 1st-degree filter
   GROUP BY l.id
   ORDER BY share_count DESC
   LIMIT 50
   ↓
5. For each link: GetLinkSharers(linkID) → avatars
   ↓
6. Return JSON response
```

**Key Files**:
- `cmd/api/main.go`: API handler
- `internal/aggregator/aggregator.go`: Ranking logic
- `internal/database/db.go`: Trending queries

---

### Workflow 4: 2nd-Degree Network Discovery

```
1. User runs: go run cmd/crawl-network/main.go
   ↓
2. Crawler syncs 1st-degree follows:
   a. GetFollowsWithMetadata(myHandle)
   b. For each follow: UpsertNetworkAccount(did, handle, ..., degree=1, sourceCount=1)
   ↓
3. Crawler crawls 2nd-degree:
   a. For each 1st-degree account (343 accounts):
      - GetFollowsWithMetadata(handle) → 2nd-degree candidates
      - Rate limit: 5 req/sec
   b. Aggregate candidates:
      - Count how many 1st-degree accounts follow each candidate
      - Track source DIDs
   c. Filter: Keep if sourceCount >= minSources (e.g., 2+)
   d. UpsertNetworkAccount(did, ..., degree=2, sourceCount, sourceDIDs)
   ↓
4. DID Manager reloads from DB: LoadFromDatabase()
   ↓
5. Now firehose accepts posts from 2nd-degree accounts
```

**Key Files**:
- `cmd/crawl-network/main.go`: Crawler orchestration
- `internal/crawler/crawler.go`: Crawl logic
- `internal/didmanager/manager.go`: DID tracking

---

### Workflow 5: Metadata Fetching

**Primary (96%): Bluesky Embed Metadata**
```
Post has embed.external:
  {
    "uri": "https://example.com",
    "title": "Article Title",
    "description": "...",
    "thumb": "https://cdn.bsky.app/..."
  }
  ↓
Immediately call: UpdateLinkMetadata(linkID, title, description, thumb)
```

**Fallback (4%): OpenGraph Scraping**
```
Post has URL but no embed metadata
  ↓
Queue for async scraping: fetchOGDataAsync(linkID, url)
  ↓
scraper.FetchOGData(url):
  1. Try HTTP/2 client
  2. If fails, try HTTP/1.1 client
  3. Parse HTML for <meta property="og:*"> tags
  4. Fallback to <title> and <meta name="description">
  5. Exponential backoff retry (3 attempts)
  ↓
UpdateLinkMetadata(linkID, title, description, imageURL)
```

**Key Files**:
- `internal/processor/processor.go`: Metadata routing
- `internal/scraper/scraper.go`: OpenGraph scraping

---

## 9. Key Files to Read First

### Understanding Post Ingestion
1. **`cmd/firehose/main.go`** - Real-time event receiver
2. **`internal/processor/processor.go`** - SINGLE shared processing pipeline
3. **`internal/didmanager/manager.go`** - Degree-based filtering

### Understanding Trending Algorithm
1. **`cmd/api/main.go`** - API endpoint handlers
2. **`internal/aggregator/aggregator.go`** - Ranking strategies
3. **`internal/database/db.go`** - Trending queries (lines 200-300)

### Understanding Network Discovery
1. **`cmd/crawl-network/main.go`** - Crawler entry point
2. **`internal/crawler/crawler.go`** - 2nd-degree discovery logic
3. **`internal/database/db.go`** - Network account management (lines 400-500)

### Understanding Metadata Fetching
1. **`internal/processor/processor.go`** - Metadata routing (lines 100-200)
2. **`internal/scraper/scraper.go`** - OpenGraph scraping
3. **`cmd/metadata-fetcher/main.go`** - Batch fetcher for gaps

### Understanding Configuration
1. **`internal/config/config.go`** - Centralized config
2. **`.env.example`** - Environment variable reference
3. **`config/config.example.yaml`** - Config file template

---

## 10. Development Quick Start

### Initial Setup

```bash
# 1. Clone repository
git clone https://github.com/petroleumjelliffe/bluesky-news-aggregator.git
cd bluesky-news-aggregator

# 2. Install Go 1.21+
go version  # Verify

# 3. Install PostgreSQL 14+
createdb bluesky_news

# 4. Configure
cp config/config.example.yaml config/config.yaml
# Edit config/config.yaml with your credentials

# 5. Run migrations
go run cmd/migrate/main.go

# 6. Sync your follows
go run cmd/crawl-network/main.go

# 7. Backfill historical data (optional)
go run cmd/backfill/main.go

# 8. Start firehose (terminal 1)
go run cmd/firehose/main.go

# 9. Start API (terminal 2)
go run cmd/api/main.go

# 10. Visit http://localhost:8080
```

### Build Commands

```bash
make build          # Build all binaries → bin/
make run-api        # Run API server
make run-firehose   # Run firehose worker
make migrate        # Run migrations
make test           # Run tests
make fmt            # Format code
make db-reset       # Reset database (destructive!)
```

### Testing

```bash
# Run all tests
go test ./...

# Test specific package
go test ./internal/processor

# Test with coverage
go test -cover ./...

# Benchmark
go test -bench=. ./internal/urlutil
```

### Debugging

**Common Issues**:

1. **"Failed to connect to database"**
   - Check PostgreSQL: `pg_isready`
   - Verify credentials in config
   - Check database exists: `psql -l | grep bluesky_news`

2. **"Authentication failed" (Bluesky)**
   - Use app password, not main password
   - Generate at: https://bsky.app/settings/app-passwords
   - Verify handle format: `yourname.bsky.social`

3. **"No links showing up"**
   - Check firehose is running and connected
   - Verify network accounts: `SELECT COUNT(*) FROM network_accounts;`
   - Check recent posts: `SELECT COUNT(*) FROM posts WHERE created_at > NOW() - INTERVAL '1 hour';`

4. **"DID not found" (Jetstream events)**
   - Run: `go run cmd/crawl-network/main.go` to refresh network
   - Check: `SELECT degree, COUNT(*) FROM network_accounts GROUP BY degree;`

**Useful Queries**:

```sql
-- Network stats
SELECT degree, COUNT(*) as count FROM network_accounts GROUP BY degree;

-- Recent posts by degree
SELECT author_degree, COUNT(*) FROM posts
WHERE created_at > NOW() - INTERVAL '24 hours'
GROUP BY author_degree;

-- Top shared links
SELECT l.normalized_url, COUNT(*) as shares
FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
WHERE p.created_at > NOW() - INTERVAL '24 hours'
GROUP BY l.id, l.normalized_url
ORDER BY shares DESC
LIMIT 10;

-- Links without metadata
SELECT COUNT(*) FROM links WHERE title IS NULL;
```

---

## 11. Deployment Notes

### Current Production Architecture (Render)

**Services**:
1. **API Service** (`cmd/api`)
   - Type: Web Service
   - Port: 8080
   - Health check: `GET /health`
   - Auto-deploy: On push to main

2. **Firehose Worker** (`cmd/firehose`)
   - Type: Background Worker
   - No exposed port
   - Auto-restart on failure

3. **Janitor Cron Job** (`cmd/janitor`)
   - Type: Cron Job
   - Schedule: Daily at 2:00 AM UTC
   - Command: `./bin/janitor`

4. **PostgreSQL Database**
   - Type: Render PostgreSQL
   - Plan: Starter ($7/month)
   - Backups: Daily automatic

**Environment Variables** (set in Render dashboard):
- `BLUESKY_HANDLE`
- `BLUESKY_PASSWORD`
- `DATABASE_URL` (auto-set by Render)
- `CORS_ALLOW_ORIGIN`

**Build Commands**:
```bash
# Render uses render.yaml for configuration
make build  # Builds all binaries to bin/
```

### Monitoring

**Health Checks**:
- API: `GET /health` every 60 seconds
- Firehose: Render monitors process (restarts if crashes)

**Logs**:
```bash
# View logs in Render dashboard or via CLI
render logs -s api-service
render logs -s firehose-worker
```

**Key Metrics to Watch**:
- Firehose events/sec (should be 5-20 during peak hours)
- API response time (should be <100ms for trending)
- Database connections (should stay below pool limit)
- Jetstream cursor lag (should update every 10s)

---

## 12. Recent Changes & Known Issues

### Recent Major Changes (v2.0.0)
- Added 2nd-degree network discovery (49k+ accounts)
- Migrated from polling to Jetstream firehose
- Implemented shared processing pipeline (`internal/processor`)
- Added degree-based filtering to API
- Fixed race condition in `GetOrCreateLink()` with atomic upsert

### Known Issues
- [ ] No indexes on `(created_at, author_degree)` yet (performance concern for large queries)
- [ ] Rate limiting is per-IP only (no per-user limits)
- [ ] No WebSocket support for real-time frontend updates
- [ ] Metadata fetcher can be slow for sites with strict rate limits
- [ ] No monitoring/alerting for firehose lag

### Technical Debt
- [ ] Add comprehensive test coverage (currently minimal)
- [ ] Implement Redis caching for trending queries
- [ ] Add Prometheus metrics export
- [ ] Implement recency-weighted and velocity ranking strategies
- [ ] Add database connection pooling tuning
- [ ] Implement graceful shutdown for all services

---

## 13. Contributing Guidelines

### Code Style
- Follow Go conventions (`go fmt`, `golint`)
- Use meaningful variable names
- Add comments for exported functions
- Keep functions small and focused

### Adding New Features

**Before Starting**:
1. Read relevant ADRs in `docs/adr/`
2. Check ROADMAP.md for context
3. Review this PROJECT_CONTEXT.md

**Development Process**:
1. Create feature branch: `git checkout -b feature/your-feature`
2. Make changes (follow existing patterns)
3. Test locally
4. Update PROJECT_CONTEXT.md if adding new functions/packages
5. Commit with descriptive message
6. Push and create PR

**Testing Checklist**:
- [ ] Unit tests for new functions
- [ ] Integration test with real database
- [ ] Manual testing with local API
- [ ] Check logs for errors/warnings
- [ ] Verify no performance regression

---

## 14. Quick Reference Tables

### Package Import Paths

| Package | Import Path |
|---------|-------------|
| Config | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/config` |
| Database | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/database` |
| Bluesky Client | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky` |
| Processor | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/processor` |
| DID Manager | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/didmanager` |
| Crawler | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/crawler` |
| Jetstream | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/jetstream` |
| Scraper | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/scraper` |
| Aggregator | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/aggregator` |
| URL Util | `github.com/petroleumjelliffe/bluesky-news-aggregator/internal/urlutil` |

### Database Connection Strings

**Development**:
```
postgresql://postgres:password@localhost:5432/bluesky_news?sslmode=disable
```

**Production** (Render auto-sets `DATABASE_URL`):
```
postgresql://user:pass@host.render.com:5432/dbname?sslmode=require
```

### Useful Links
- **Bluesky API Docs**: https://docs.bsky.app/
- **Jetstream Docs**: https://github.com/bluesky-social/jetstream
- **AT Protocol Spec**: https://atproto.com/
- **Repository**: https://github.com/petroleumjelliffe/bluesky-news-aggregator
- **Issues**: https://github.com/petroleumjelliffe/bluesky-news-aggregator/issues

---

## 15. Updating This Document

**When to Update**:
- ✅ Adding new packages or functions
- ✅ Changing API endpoints or parameters
- ✅ Modifying database schema
- ✅ Adding new commands or workflows
- ✅ Changing configuration options
- ✅ Completing features from ROADMAP.md

**What to Update**:
1. Section 4: Function Catalog (add new functions/types)
2. Section 3: Database Schema (if schema changes)
3. Section 6: API Endpoints (if API changes)
4. Section 8: Workflows (if new patterns emerge)
5. Section 12: Recent Changes (document what changed)

**How to Update**:
1. Edit this file: `PROJECT_CONTEXT.md`
2. Update version at top if major changes
3. Update "Last Updated" date
4. Commit with message: `docs: Update PROJECT_CONTEXT.md - [what changed]`

**Pro Tip**: AI assistants should be reminded to check this file at the start of each session and update it after completing features. Add to your Claude config or startup prompt!

---

**End of Document**

*This document should be your starting point for any work on this project. If you find gaps or inaccuracies, please update it!*
