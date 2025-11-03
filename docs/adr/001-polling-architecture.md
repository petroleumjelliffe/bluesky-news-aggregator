# ADR 001: Polling-based Data Ingestion Architecture

**Status**: Accepted (to be superseded by Jetstream relay)

**Date**: 2025-11-02

**Context**: Need to aggregate and track links shared across Bluesky network by followed accounts.

## Decision

Implement a polling-based architecture that periodically fetches posts from all followed accounts using Bluesky's AT Protocol API.

## Architecture

### Components

1. **Poller Service** (`cmd/poller/main.go`)
   - Fetches list of followed accounts via `GetFollows()`
   - Polls each account's feed using `GetAuthorFeed()`
   - Runs on 15-minute interval (configurable)
   - Concurrent processing with rate limiting (10 concurrent, 100ms delay)

2. **Bluesky Client** (`internal/bluesky/client.go`)
   - Handles authentication (JWT tokens)
   - API methods: `GetAuthorFeed()`, `GetFollows()`
   - Base URL: `https://bsky.social/xrpc`

3. **Database** (PostgreSQL)
   - Tables: `posts`, `links`, `post_links`, `poll_state`
   - Stores posts, normalized URLs, and metadata
   - Tracks polling state with cursors

4. **Scraper** (`internal/scraper/scraper.go`)
   - Fetches OpenGraph metadata from URLs
   - HTTP/2 with HTTP/1.1 fallback
   - Per-domain rate limiting (1 req/sec)
   - Exponential backoff retry (500ms, 1s)

### Data Flow

```
1. Poller starts → GetFollows() → [342 accounts]
2. For each account (concurrent):
   a. Check poll_state for cursor
   b. If no cursor → Initial ingestion (24 hours)
   c. If cursor exists → Incremental poll (since last cursor)
   d. Extract URLs from posts
   e. Fetch OpenGraph metadata (if not from Bluesky embed)
   f. Store posts, links, metadata
   g. Save new cursor
3. Sleep 15 minutes, repeat
```

### Key Features

**Cursor Management**:
- Initial ingestion: Fetches last 24 hours of posts
- Regular polling: Uses cursor to fetch only new posts
- Cursor saved in `poll_state` table after each successful poll

**URL Processing**:
1. Check if post has `embed.External` with metadata
2. If metadata exists → use Bluesky's pre-fetched data
3. If not → extract URLs from text and scrape

**Error Handling**:
- Permanent errors (400, 401, 403, 404, 410): Skip account, no retry
- Transient errors (timeout, 502, 503, 504): Retry with exponential backoff
- Invalid accounts: Log `[SKIP]` instead of `[ERROR]`

## Consequences

### Positive
- Simple to understand and debug
- Works with standard AT Protocol APIs
- Cursor-based pagination prevents duplicate ingestion
- Uses Bluesky's metadata when available (96% reduction in scraping)

### Negative
- 15-minute latency for new posts
- API rate limiting concerns at scale
- Polling all 342 accounts every 15 minutes (even if inactive)
- HTTP timeout issues with some domains (e.g., Washington Post)

## Implementation Details

**Configuration** (`config/config.yaml`):
```yaml
polling:
  interval_minutes: 15
  posts_per_page: 50
  max_concurrent: 10
  rate_limit_ms: 100
  initial_lookback_hours: 24
  max_retries: 3
  retry_backoff_ms: 1000
  max_pages_per_user: 100
```

**Performance** (342 accounts):
- Initial run: ~18 seconds (all accounts do 24h ingestion)
- Regular run: ~5-10 seconds (only active accounts logged)
- Active accounts per cycle: ~10-30 (out of 342)

## Related Issues
- Issue #2: Washington Post HTTP/2 errors (partially fixed with HTTP/1.1 fallback)
- Issue #14: Use Bluesky's pre-fetched metadata (implemented)
- Bug fix: Cursor not being saved after initial ingestion (fixed)

## Next Steps
- Migrate to Jetstream relay for real-time updates
- Eliminate polling latency
- Reduce API load
