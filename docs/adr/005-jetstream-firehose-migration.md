# ADR 005: Migrate to Jetstream Firehose for Real-time Ingestion

**Status**: Proposed

**Date**: 2025-11-02

**Related Issue**: [#11](https://github.com/petroleumjelliffe/bluesky-news-aggregator/issues/11)

**Supersedes**: ADR 001 (Polling Architecture)

## Context

Current polling architecture has fundamental limitations:
- **15-minute latency**: New posts only discovered every 15 minutes
- **Unnecessary API load**: Polling 342 accounts every cycle (even if inactive)
- **Inefficient**: Making ~342 API calls per cycle to check for new content
- **Scale concerns**: Adding more accounts linearly increases API calls

Bluesky's **Jetstream** provides a real-time firehose of all network activity as simple JSON over WebSocket, reducing bandwidth by >99% compared to the raw AT Protocol firehose.

## Decision

**Migrate from polling to Jetstream firehose with hybrid fallback**:
1. Primary: Real-time WebSocket connection to Jetstream
2. Fallback: Polling API for backfill and gap recovery
3. Data retention: 24-hour rolling window (drop old links)

## Architecture

### Overview

```
                    ┌─────────────────┐
                    │   Jetstream     │
                    │   (Public WS)   │
                    └────────┬────────┘
                             │
                    WebSocket Connection
                    wss://jetstream2.*.bsky.network
                             │
                    ┌────────▼────────┐
                    │  Firehose       │
                    │  Consumer       │
                    │  (New Service)  │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │   Filter by     │
                    │   Followed DIDs │
                    └────────┬────────┘
                             │
                ┌────────────┴────────────┐
                │                         │
        Posts from followed       DIDs not in DB?
        accounts only             │
                │                 │
                │         ┌───────▼────────┐
                │         │  Backfill      │
                │         │  Service       │
                │         │  (Polling API) │
                │         └───────┬────────┘
                │                 │
                └─────────────────┘
                          │
                ┌─────────▼─────────┐
                │  Process Post     │
                │  Extract URLs     │
                │  Store Metadata   │
                └─────────┬─────────┘
                          │
                ┌─────────▼─────────┐
                │  PostgreSQL       │
                │  (24h retention)  │
                └───────────────────┘
```

### Components

#### 1. Firehose Consumer (New)
- **Purpose**: Real-time event ingestion
- **Location**: `cmd/firehose/main.go`
- **Connection**: WebSocket to Jetstream public instance
- **Filtering**: `wantedDids` parameter (list of followed accounts)
- **Collections**: `app.bsky.feed.post` (posts only)
- **Compression**: zstd for bandwidth reduction (~56% savings)

#### 2. DID Manager (New)
- **Purpose**: Track which DIDs (accounts) to follow
- **Source**: `GetFollows()` API (refresh periodically)
- **Storage**: In-memory set of DIDs + database table
- **Update frequency**: Every 1 hour (configurable)
- **Dynamic updates**: Send `options_update` to WebSocket when follows change

#### 3. Backfill Service (Modified Poller)
- **Purpose**: Fill gaps and initialize new follows
- **Triggers**:
  - New DID appears in follows list (24h backfill)
  - Connection drops (gap recovery from `time_us` cursor)
  - Manual backfill request
- **Rate limiting**: Same as current poller (avoid API abuse)

#### 4. Data Retention Service (New)
- **Purpose**: Delete links older than 24 hours
- **Frequency**: Every 1 hour (configurable)
- **Query**: `DELETE FROM links WHERE id NOT IN (SELECT DISTINCT link_id FROM post_links pl JOIN posts p ON pl.post_id = p.id WHERE p.created_at > NOW() - INTERVAL '24 hours')`

## Jetstream Configuration

### Connection Details

**Public Endpoints**:
- Primary: `wss://jetstream2.us-west.bsky.network/subscribe`
- Fallback: `wss://jetstream2.us-east.bsky.network/subscribe`

**WebSocket URL**:
```
wss://jetstream2.us-west.bsky.network/subscribe?wantedCollections=app.bsky.feed.post&compress=true
```

**Initial connection**: No DIDs filter (subscribe to all, filter client-side initially)

**After DID list loaded**:
```json
{
  "type": "options_update",
  "payload": {
    "wantedDids": ["did:plc:abc...", "did:plc:xyz...", ...],
    "wantedCollections": ["app.bsky.feed.post"]
  }
}
```

### Message Format

**Post Event**:
```json
{
  "did": "did:plc:eygmaihciaxprqvxpfvl6flk",
  "time_us": 1725911162329308,
  "kind": "commit",
  "commit": {
    "rev": "3l3qo2vutsw2b",
    "operation": "create",
    "collection": "app.bsky.feed.post",
    "rkey": "3l3qo2vuowo2b",
    "record": {
      "$type": "app.bsky.feed.post",
      "text": "Check this out https://example.com",
      "createdAt": "2024-09-09T19:46:02.102Z",
      "embed": {
        "$type": "app.bsky.embed.external",
        "external": {
          "uri": "https://example.com",
          "title": "Example Title",
          "description": "Example description",
          "thumb": "..."
        }
      }
    }
  }
}
```

**Identity/Account Events**:
- Ignored for link aggregation (not relevant to our use case)

## Implementation Plan

### Phase 1: Core Firehose Consumer (Week 1)

**New files**:
- `cmd/firehose/main.go` - Main service entry point
- `internal/jetstream/client.go` - WebSocket client
- `internal/jetstream/types.go` - Event types
- `internal/didmanager/manager.go` - DID tracking

**Database changes**:
```sql
-- New table for tracking follows (replaces poll_state functionality)
CREATE TABLE IF NOT EXISTS follows (
    did TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    display_name TEXT,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP,
    backfill_completed BOOLEAN DEFAULT FALSE
);

-- Index for quick DID lookups
CREATE INDEX idx_follows_did ON follows(did);
CREATE INDEX idx_follows_handle ON follows(handle);

-- New table for cursor persistence (alternative to file)
CREATE TABLE IF NOT EXISTS jetstream_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    cursor_time_us BIGINT NOT NULL,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT single_row CHECK (id = 1)
);
```

**Migration from poll_state**:
- Phase 1: Create `follows` table, keep `poll_state` unchanged
- Phase 2: Populate `follows` from `poll_state.user_handle` + DID resolution
- Phase 3: Use `follows` table, stop writing to `poll_state`
- Phase 4: Truncate `poll_state` (keep for rollback), drop in Phase 5
- Rollback plan: Re-populate `poll_state` from `follows` if needed

**Features**:
- WebSocket connection with reconnect logic
- zstd decompression (using shared dictionary)
- Event parsing and validation
- Post processing (extract URLs, metadata)
- Cursor persistence for replay

**Technical Decisions**:

**Jetstream Client**: `github.com/bluesky-social/jetstream/pkg/client`
- **Purpose**: WebSocket connection to compressed Jetstream firehose
- **Features**:
  - Built-in zstd decompression (uses embedded dictionary from `pkg/models`)
  - Compression enabled by default: `ClientConfig{Compress: true}`
  - ~56% bandwidth reduction (482 bytes → 211 bytes per event)
  - Automatic reconnection and cursor management
- **Configuration**:
  ```go
  config := jetstream.ClientConfig{
      WebsocketURL: "wss://jetstream2.us-west.bsky.network/subscribe",
      Compress: true,  // Enable zstd compression
      WantedCollections: []string{"app.bsky.feed.post"},
      WantedDids: followedDIDs,  // Filter to followed accounts
  }
  ```

**Indigo Library**: `github.com/bluesky-social/indigo`
- **Purpose**: API calls for backfill and DID resolution (NOT firehose)
- **Key packages**:
  - `indigo/xrpc`: XRPC client for API calls (replaces custom `internal/bluesky/client.go`)
  - `indigo/api/bsky`: Generated lexicon types for `app.bsky.*` APIs
  - `indigo/atproto/identity`: DID and handle resolution
- **Trade-off**: "Unstable interfaces under active development" per docs
- **Benefit**: Stays in sync with AT Protocol changes, battle-tested in production

**Why Two Libraries?**:
- **Jetstream** provides simplified JSON events (easy to parse, compressed)
- **Indigo** provides full AT Protocol API access (for backfill, DID resolution)
- Jetstream is for real-time ingestion; Indigo is for API calls
- Both are official Bluesky libraries

**Compression Details**:
- Dictionary: Embedded in Jetstream client (no manual download needed)
- Format: zstd with custom dictionary optimized for AT Protocol events
- Protocol: WebSocket with `compress=true` parameter or `Socket-Encoding: zstd` header
- Performance: ~0.44 compression ratio (highly efficient)

**Cursor Persistence**:
- Storage: File at `data/jetstream_cursor.txt`
- Format: Single line with Unix microseconds timestamp (e.g., `1725911162329308`)
- Update frequency: Every 10 seconds (configurable)
- On startup: Read cursor, pass to Jetstream via `?cursor=` parameter
- Crash recovery: Jetstream replays from cursor (up to 24h buffer)

**Message Type Handling**:
- `create`: Process normally (new post with URLs)
- `update`: Re-process post, update metadata if changed
  - Edge case: URL added in edit → extract and store
- `delete`: Remove post from database
  - CASCADE deletes post_links
  - Links remain if shared by other posts
  - Trending count decreases automatically

**Identity/Account Events**:
- `identity`: DID document changed (ignore, DIDs don't change)
- `account` (deactivated): Remove from `follows` table, stop filtering
- `account` (takendown): Same as deactivated
- Implementation: Check event kind, call `didManager.RemoveDID(did)`

**Error Handling**:
- Malformed JSON: Log error, skip event, continue stream
- Failed URL extraction: Log, continue (don't crash service)
- Failed metadata fetch: Already handled by scraper (non-fatal)
- WebSocket disconnect: Auto-reconnect with exponential backoff (5s, 10s, 30s, 60s max)
- Persistent failure: Alert after 10 failed reconnects

**Testing**: Run alongside poller, compare ingestion

### Phase 2: DID Management (Week 1-2)

**Features**:
- Periodic `GetFollows()` refresh (1 hour)
- Convert handles → DIDs (via `app.bsky.actor.getProfile`)
- Send `options_update` to WebSocket when follows change
- Trigger backfill for new DIDs

**DID Resolution Strategy**:
1. Call `GetFollows()` using `indigo/xrpc` client → returns list of handles
2. Batch resolve handles → DIDs using `indigo/atproto/identity` package
   - Use `identity.ResolveHandle()` for individual lookups
   - Batch size: 25 handles per request (rate limit consideration)
   - Cache handle→DID mapping in `follows` table
3. Store DIDs in database with handles for reference
4. Update in-memory DID set for filtering

**Migration from Custom Client**:
- Replace `internal/bluesky/client.go` with `indigo/xrpc.Client`
- Use generated types from `indigo/api/bsky` instead of manual JSON structs
- Benefit: Automatic updates when Bluesky adds new lexicon fields

**Handle Changes**:
- If handle changes but DID stays same: Update `follows.handle` column
- If account deleted: Remove from `follows` table, stop filtering
- Refresh mechanism: Compare GetFollows() result with database, sync differences

**Challenges**:
- Handle limit: 10,000 DIDs max per filter
- Current follows: 342 (well under limit)
- Future: Consider sharding if > 10k follows

### Phase 3: Backfill Service (Week 2)

**Modify existing poller**:
- Convert to on-demand backfill service
- Accept DID + lookback period parameters
- Remove periodic scheduling (triggered by firehose)

**Triggers**:
1. New DID detected: Backfill last 24 hours
2. Gap detected: Backfill from `time_us` cursor to last known event
3. Cold start: Backfill all followed DIDs (last 24h each)

**Gap Detection Logic**:
- Compare incoming `time_us` with last persisted cursor
- If gap > 5 minutes (300,000,000 microseconds): Trigger backfill
- If gap < 5 minutes: Trust Jetstream's 24h buffer, no backfill needed
- Rationale: Brief disconnects covered by Jetstream replay, long gaps need API backfill

**Backfill Queue**:
- Priority: Gaps > New DIDs > Cold start
- Rate limiting: Share same limits as original poller (10 concurrent, 100ms delay)
- Deduplication: Skip if DID already in backfill queue

### Phase 4: Data Retention (Week 2)

**New service**: `cmd/janitor/main.go`

**Features**:
- Hourly cleanup of old data
- Delete cascade: links → post_links → posts
- Keep trending_links view efficient
- Configurable retention period (default: 24h)

**Queries**:
```sql
-- Delete posts older than 24h
DELETE FROM posts
WHERE created_at < NOW() - INTERVAL '24 hours';

-- Delete links with no recent posts, EXCEPT trending links (5+ shares)
-- This keeps viral links even if they're older than 24h
DELETE FROM links
WHERE id NOT IN (
  SELECT DISTINCT link_id
  FROM post_links pl
  JOIN posts p ON pl.post_id = p.id
  WHERE p.created_at > NOW() - INTERVAL '24 hours'
)
AND id NOT IN (
  -- Keep links with 5+ shares (trending threshold)
  SELECT link_id
  FROM post_links
  GROUP BY link_id
  HAVING COUNT(*) >= 5
);

-- Clean poll_state (no longer needed with firehose)
-- Keep table structure for potential rollback, just truncate data
TRUNCATE poll_state;
```

**Trending Links Exception**:
- Links with 5+ shares are kept regardless of age
- If a trending link drops below 5 shares: Grace period of 24h before deletion
- Implementation: Check share count in janitor, only delete if both conditions met:
  1. No posts in last 24h AND
  2. Total share count < 5

### Phase 5: Monitoring & Deployment (Week 3)

**Metrics to track**:
- WebSocket connection uptime (%)
- Events processed per second (rate)
- Backfill queue depth (count)
- Database size (should stabilize ~1-2 GB)
- Latency: Jetstream event → database insert (p50, p99)
- Cursor lag: Current time - last cursor time (seconds)

**Monitoring Implementation**:
- **Logging**: Structured JSON logs to stdout
  - Level: info, warn, error
  - Fields: timestamp, component, event_type, metadata
- **Health endpoint**: HTTP server on `:8081/health`
  ```json
  {
    "status": "healthy",
    "websocket_connected": true,
    "last_event_seconds_ago": 2.5,
    "cursor_time_us": 1725911162329308,
    "follows_count": 342,
    "backfill_queue_depth": 0
  }
  ```
- **Alerting** (manual monitoring initially):
  - WebSocket disconnected > 5 minutes
  - No events received > 2 minutes
  - Backfill queue > 50 items
  - Database size > 3 GB (runaway growth)
  - Failed reconnects > 10 attempts

**Deployment**:
- **Phase 1 (Local M1 Mac)**:
  - Run via `make run-firehose`
  - Logs to console
  - Manual restart if needed
  - Data dir: `./data/` (cursor file, logs)

- **Phase 2 (Production)**:
  - Docker Compose setup (future)
  - systemd service or launchd daemon
  - Log aggregation (file rotation)
  - Auto-restart on crash

**Cold Start Performance**:
- 342 accounts × 24h backfill each
- Sequential processing: ~34 seconds per account
- Total time: ~3 hours (342 × 34s / 60 / 60)
- **Optimization**: Run 10 concurrent backfills → ~20 minutes
- **Strategy**: Start firehose first (real-time), backfill in background
- User experience: Real-time updates immediate, historical data fills in gradually

## Dependency Management

**Required Libraries**:
```go
// go.mod
require (
    github.com/bluesky-social/jetstream v0.0.0-YYYYMMDD-HASH
    github.com/bluesky-social/indigo v0.0.0-YYYYMMDD-HASH
)
```

**Version Pinning Strategy**:

**Jetstream Client**:
1. **Pin to specific commit** - Use `go get github.com/bluesky-social/jetstream@HASH`
2. **Update for compression improvements** - Jetstream may optimize dictionary or protocol
3. **Monitor releases** - Watch for breaking changes in client API
4. **Test compression** - Verify bandwidth savings after upgrades

**Indigo Library**:
1. **Pin to specific commit** - Use `go get github.com/bluesky-social/indigo@HASH`
2. **Test before upgrading** - Run backfill tests against new version
3. **Monitor lexicon changes** - New fields in `app.bsky.*` may require code updates
4. **Accept breaking changes carefully** - Review diffs before updating

**Rationale**: Both are marked "unstable under active development". Pinning lets us control when to absorb API changes rather than breaking unexpectedly on `go get -u`.

**Upgrade Cadence**: Review monthly, upgrade quarterly (unless critical fix needed)

## Configuration

```yaml
jetstream:
  # Primary endpoint
  endpoint: wss://jetstream2.us-west.bsky.network/subscribe
  # Fallback endpoint
  fallback_endpoint: wss://jetstream2.us-east.bsky.network/subscribe

  # Compression (reduces bandwidth ~56%)
  compress: true

  # Collections to subscribe to
  wanted_collections:
    - app.bsky.feed.post

  # Reconnection settings
  reconnect_delay_seconds: 5
  max_reconnect_attempts: 0  # 0 = infinite

  # Cursor persistence
  cursor_save_interval_seconds: 10
  cursor_file: data/jetstream_cursor.txt

did_manager:
  # How often to refresh follows list
  refresh_interval_minutes: 60

  # Backfill new follows
  backfill_enabled: true
  backfill_lookback_hours: 24

retention:
  # Data retention period
  window_hours: 24

  # Cleanup frequency
  cleanup_interval_minutes: 60

  # Keep links that are still trending even if > 24h
  keep_trending_threshold: 5  # Keep if shared 5+ times

backfill:
  # Same as polling config
  posts_per_page: 50
  max_concurrent: 10
  rate_limit_ms: 100
  max_retries: 3
```

## Bandwidth & Storage Estimates

### Bandwidth (M1 Mac - Local)

**Jetstream (compressed)**:
- All posts: ~850 MB/day
- Filtered to 342 followed DIDs: ~10-50 MB/day (estimate)
- Monthly: 300 MB - 1.5 GB

**Backfill API calls**:
- New follows: Rare (< 1 GB/month)
- Gap recovery: Minimal (< 100 MB/month)

**Total**: ~2-3 GB/month (vs ~10-15 GB with polling)

### Storage (PostgreSQL)

**Current (with polling)**:
- Unbounded growth (~1 GB/week)
- No cleanup strategy

**With 24h retention**:
- ~50-150 posts/hour × 342 accounts = ~8,000 posts/day
- ~20-80 links/hour = ~2,000 unique links/day
- Database size: **Stable at 1-2 GB** (24h rolling window)

**Backups**: Not needed initially (24h window = acceptable data loss)

### CPU/Memory (M1 Mac)

**Firehose consumer**:
- zstd decompression: Negligible (<1% CPU)
- Event processing: ~100-500 events/sec (minimal load)
- Memory: ~50-100 MB resident

**Total system**: <200 MB RAM, <5% CPU sustained

## Advantages

### Performance
- **Real-time**: Posts appear instantly (no 15-min delay)
- **Efficient**: One WebSocket connection vs 342 API calls/cycle
- **Scalable**: Follow 10,000 accounts with same bandwidth

### Cost
- **Bandwidth**: 2-3 GB/month (vs 10-15 GB with polling)
- **API load**: ~95% reduction in API calls
- **Storage**: Stable size (vs unbounded growth)

### Features
- **Network-wide trends**: Can expand beyond followed accounts
- **Real-time notifications**: Immediate link discovery
- **Better filtering**: Collection-level (posts, likes, reposts)

## Disadvantages

### Complexity
- **WebSocket management**: Reconnection logic, cursor persistence (mitigated by Indigo)
- **New failure modes**: Network drops, Jetstream outages
- **More services**: Firehose + backfill + janitor vs single poller
- **Unstable dependency**: Indigo under active development with unstable interfaces

### Trust
- **No cryptographic verification**: Must trust Jetstream provider
- **Not in protocol**: Jetstream could change or disappear
- **Mitigation**: Can self-host Jetstream if needed
- **Library stability**: Indigo API may break with updates (pin to specific version)

### Storage
- **Data loss**: 24h window means historical data is lost
- **No backfill**: Can't easily re-fetch old posts
- **Mitigation**: Acceptable for trending/aggregation use case

## Migration Strategy

### Parallel Run (2 weeks)
1. **Week 1**: Deploy firehose consumer alongside poller
2. Compare ingestion: Same posts found? Same metadata?
3. Monitor reliability: Connection uptime? Gap recovery?

### Cutover (Week 2)
1. Verify firehose catches all posts
2. Stop poller service
3. Enable data retention cleanup
4. Monitor for 48 hours

### Rollback Plan
- Keep poller code functional
- Switch back if critical issues
- Database schema compatible with both

## Alternatives Considered

### 1. Custom WebSocket Client + Manual zstd (Rejected)
**Approach**: Build our own firehose consumer using `nhooyr.io/websocket` + `klauspost/compress/zstd`

**Pros**:
- Full control over implementation
- Could customize compression parameters

**Cons**:
- Must manually download and manage zstd dictionary
- Reinventing reconnect logic, cursor persistence
- No bandwidth savings without compression working correctly
- Manual JSON parsing (fragile when Jetstream schema changes)
- More code to maintain

**Decision**: Use official Jetstream client. It includes zstd compression with embedded dictionary, automatic reconnection, and cursor management. The "unstable" risk is mitigated by version pinning.

### 2. Community Libraries (e.g., karalabe/go-bluesky)
**Approach**: Use third-party wrapper libraries

**Pros**:
- Higher-level API, easier to use
- Stable interfaces

**Cons**:
- Not official, may lag behind protocol changes
- Less comprehensive than Indigo
- No firehose support

**Decision**: Use official Indigo library for protocol-level work.

### 3. AT Protocol Firehose (via Indigo) Instead of Jetstream (Rejected)
**Approach**: Use `indigo/events` to consume raw AT Protocol firehose (repo commits)

**Pros**:
- Cryptographically verified (repo signatures)
- Official protocol (not a convenience layer)
- More control over data validation

**Cons**:
- **No compression**: Raw CAR files + CBOR encoding, much larger bandwidth
- **Complex parsing**: Must decode repo commits, extract records from CAR files
- **More code**: Would need to implement record extraction, validation
- **Overkill**: We don't need cryptographic verification for link aggregation

**Decision**: Use Jetstream for simplified JSON + zstd compression. We trust Jetstream provider (Bluesky) and prioritize bandwidth efficiency.

### 4. Keep Polling, No Firehose (Rejected)
**Approach**: Optimize current polling architecture

**Pros**:
- Simple, working today
- No new dependencies
- Known failure modes

**Cons**:
- 15-minute delay (user wants real-time)
- Unbounded storage growth (user wants 24h retention)
- High API load (342 calls every 15min)
- Can't scale beyond followed accounts

**Decision**: Migrate to firehose to meet real-time and storage requirements.

## Future Enhancements

### Self-hosting
- Run local Jetstream instance for control
- Requires more resources (~$5-20/month VPS)
- Full network visibility (not just follows)

### Advanced Filtering
- Track specific hashtags
- Language filtering
- Spam detection

### Multiple Consumers
- Separate service for notifications
- Analytics pipeline
- Moderation tools

## Open Questions

1. **DID limit**: What if we want to follow > 10,000 accounts?
   - Answer: Shard across multiple WebSocket connections
   - Current: 342 accounts (well under limit)

2. **Cursor persistence**: Where to store for crash recovery?
   - Option A: File on disk (`data/jetstream_cursor.txt`)
   - Option B: Database table
   - Recommendation: Start with file, move to DB if needed

3. **Backfill strategy**: When to trigger?
   - New follow: Always (24h backfill)
   - Gap > 5 min: Backfill from cursor
   - Gap < 5 min: Replay from Jetstream buffer

4. **Data retention edge case**: Keep trending links > 24h?
   - Yes: If shared 5+ times, keep until drops below threshold
   - Prevents losing viral links

## Success Metrics

- [ ] Latency: Posts appear in DB within 1 second
- [ ] Uptime: > 99% WebSocket connection time
- [ ] Completeness: No missing posts vs old poller
- [ ] Database size: Stable at < 2 GB
- [ ] Bandwidth: < 3 GB/month

## References

- [Jetstream GitHub](https://github.com/bluesky-social/jetstream)
- [Jetstream Blog Post](https://jazco.dev/2024/09/24/jetstream/)
- [Bluesky Docs: Jetstream](https://docs.bsky.app/blog/jetstream)
- [Bluesky Docs: Firehose](https://docs.bsky.app/docs/advanced-guides/firehose)
