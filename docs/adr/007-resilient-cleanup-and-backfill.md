# ADR 007: Resilient Cleanup and Backfill Strategy

**Status**: Proposed
**Date**: 2025-11-20
**Author**: Claude Code + Pete Jelliffe

## Context

The current cleanup and backfill implementation has several issues:

1. **Janitor runs manually** - No automated cleanup, database grows unbounded
2. **Cursor updates too frequently** - Updates on every event (100-500/sec)
3. **No trending link exception** - Deletes popular links after retention period
4. **Retention period misalignment** - Posts (30d) vs Links (90d)
5. **No cleanup on startup** - Services start with stale data
6. **Backfill goes too far back** - Can fetch months of data if flag is reset

## Requirements

From user discussion:

1. **Empty outdated links** - Delete links not shared within last 24 hours
2. **Smart backfill** - Fill gap from cursor to now, max 24 hours
3. **Startup cleanup** - Clean stale data before service starts
4. **Ongoing cleanup** - Periodic maintenance while running

## Decision

Implement a three-phase cleanup and maintenance strategy:

### Phase 1: Startup Cleanup (Before Firehose Starts)

Run on firehose/API startup to ensure clean slate:

```go
func StartupCleanup(db *database.DB) error {
    log.Println("[STARTUP] Running cleanup procedures...")

    // 1. Delete posts older than 24 hours
    cutoff := time.Now().Add(-24 * time.Hour)
    postsDeleted, err := db.DeleteOldPosts(cutoff)
    if err != nil {
        return fmt.Errorf("failed to delete old posts: %w", err)
    }
    log.Printf("[STARTUP] Deleted %d old posts (>24h)", postsDeleted)

    // 2. Delete orphaned post_links (safety cleanup)
    orphansDeleted, err := db.DeleteOrphanedPostLinks()
    if err != nil {
        return fmt.Errorf("failed to delete orphaned links: %w", err)
    }
    log.Printf("[STARTUP] Deleted %d orphaned post_links", orphansDeleted)

    // 3. Delete links with no recent shares (last 24h)
    linksDeleted, err := db.DeleteUnsharedLinks(cutoff)
    if err != nil {
        return fmt.Errorf("failed to delete unshared links: %w", err)
    }
    log.Printf("[STARTUP] Deleted %d unshared links (no shares in 24h)", linksDeleted)

    // 4. Keep trending links (5+ shares, regardless of age)
    // This is handled in the DeleteUnsharedLinks query via exception

    log.Println("[STARTUP] Cleanup complete")
    return nil
}
```

### Phase 2: Smart Backfill (Fill Cursor Gap)

Run after startup cleanup, before firehose connects:

```go
func SmartBackfill(db *database.DB, client *bluesky.Client) error {
    log.Println("[BACKFILL] Starting smart backfill...")

    // 1. Get current cursor
    cursor, err := db.GetJetstreamCursor()
    if err != nil {
        return fmt.Errorf("failed to get cursor: %w", err)
    }

    // 2. Calculate cursor age
    var cursorTime time.Time
    if cursor != nil {
        cursorTime = time.UnixMicro(*cursor)
    } else {
        // No cursor = first run, backfill 24 hours
        cursorTime = time.Now().Add(-24 * time.Hour)
    }

    cursorAge := time.Since(cursorTime)

    // 3. Determine backfill strategy
    if cursorAge > 24*time.Hour {
        log.Printf("[BACKFILL] Cursor is %v old (>24h), limiting backfill to 24h", cursorAge)
        cursorTime = time.Now().Add(-24 * time.Hour)
    } else if cursorAge > 5*time.Minute {
        log.Printf("[BACKFILL] Cursor is %v old, backfilling gap", cursorAge)
    } else {
        log.Printf("[BACKFILL] Cursor is fresh (%v), skipping backfill", cursorAge)
        return nil
    }

    // 4. Backfill only active follows (seen in last 7 days)
    follows, err := db.GetActiveFollows(7 * 24 * time.Hour)
    if err != nil {
        return fmt.Errorf("failed to get active follows: %w", err)
    }

    log.Printf("[BACKFILL] Backfilling %d active accounts from %v to now",
        len(follows), cursorTime)

    // 5. Run limited backfill
    for _, follow := range follows {
        if err := backfillAccount(client, db, follow, cursorTime); err != nil {
            log.Printf("[BACKFILL] Error backfilling %s: %v", follow.Handle, err)
            continue
        }
    }

    log.Println("[BACKFILL] Smart backfill complete")
    return nil
}
```

### Phase 3: Ongoing Cleanup (Background Task)

Run periodically while services are running:

```go
func StartCleanupTicker(db *database.DB, interval time.Duration) {
    ticker := time.NewTicker(interval)

    go func() {
        for range ticker.C {
            if err := periodicCleanup(db); err != nil {
                log.Printf("[CLEANUP] Periodic cleanup failed: %v", err)
            }
        }
    }()

    log.Printf("[CLEANUP] Started periodic cleanup (interval: %v)", interval)
}

func periodicCleanup(db *database.DB) error {
    log.Println("[CLEANUP] Running periodic cleanup...")

    cutoff := time.Now().Add(-24 * time.Hour)

    // 1. Delete old posts
    postsDeleted, err := db.DeleteOldPosts(cutoff)
    if err != nil {
        return err
    }

    // 2. Delete unshared links
    linksDeleted, err := db.DeleteUnsharedLinks(cutoff)
    if err != nil {
        return err
    }

    // 3. Batch update cursor (reduce write pressure)
    // Already handled by cursor batching in firehose

    log.Printf("[CLEANUP] Deleted %d posts, %d links", postsDeleted, linksDeleted)
    return nil
}
```

## Database Methods

### 1. DeleteOldPosts

```sql
-- Delete posts older than cutoff
-- Cascades to post_links via ON DELETE CASCADE
DELETE FROM posts
WHERE created_at < $1
RETURNING id;
```

### 2. DeleteOrphanedPostLinks

```sql
-- Safety cleanup: remove post_links with missing posts or links
DELETE FROM post_links
WHERE post_id NOT IN (SELECT id FROM posts)
   OR link_id NOT IN (SELECT id FROM links);
```

### 3. DeleteUnsharedLinks

```sql
-- Delete links with no shares in last 24 hours
-- EXCEPT: Keep trending links (5+ total shares)
DELETE FROM links
WHERE id IN (
    -- Links with no recent shares
    SELECT l.id
    FROM links l
    LEFT JOIN post_links pl ON l.id = pl.link_id
    LEFT JOIN posts p ON pl.post_id = p.id
    GROUP BY l.id
    HAVING MAX(p.created_at) < $1  -- No shares since cutoff
       AND COUNT(pl.id) < 5        -- Not trending (< 5 shares)
);
```

### 4. GetActiveFollows

```sql
-- Get follows active in last N hours
SELECT * FROM follows
WHERE last_seen_at > NOW() - INTERVAL '$1 hours'
ORDER BY last_seen_at DESC;
```

## Firehose Integration

### Startup Sequence

```go
func main() {
    // ... config loading ...

    // 1. Startup cleanup BEFORE connecting to firehose
    if err := StartupCleanup(db); err != nil {
        log.Fatalf("Startup cleanup failed: %v", err)
    }

    // 2. Smart backfill to fill cursor gap
    if err := SmartBackfill(db, bskyClient); err != nil {
        log.Fatalf("Smart backfill failed: %v", err)
    }

    // 3. Start ongoing cleanup ticker
    StartCleanupTicker(db, 1 * time.Hour)

    // 4. Start firehose with batched cursor updates
    cursor, _ := db.GetJetstreamCursor()
    if err := client.Connect(ctx, cursor); err != nil {
        log.Fatalf("Failed to connect: %v", err)
    }

    // ... event handling ...
}
```

### Cursor Batching

Reduce database write pressure by batching cursor updates:

```go
// In firehose handler
var (
    lastCursor       int64
    lastCursorUpdate time.Time
    cursorMutex      sync.Mutex
)

const cursorUpdateInterval = 10 * time.Second

handler := func(ctx context.Context, event *models.Event) error {
    // ... process event ...

    // Update cursor in memory
    cursorMutex.Lock()
    lastCursor = event.TimeUS
    cursorMutex.Unlock()

    // Periodically flush to database
    if time.Since(lastCursorUpdate) > cursorUpdateInterval {
        cursorMutex.Lock()
        cursor := lastCursor
        cursorMutex.Unlock()

        if err := db.UpdateJetstreamCursor(cursor); err != nil {
            log.Printf("[WARN] Failed to update cursor: %v", err)
        } else {
            lastCursorUpdate = time.Now()
        }
    }

    return nil
}

// Flush cursor on shutdown
defer func() {
    cursorMutex.Lock()
    cursor := lastCursor
    cursorMutex.Unlock()

    if err := db.UpdateJetstreamCursor(cursor); err != nil {
        log.Printf("[ERROR] Failed to save final cursor: %v", err)
    }
}()
```

## Configuration

Add to `config.yaml`:

```yaml
cleanup:
  # Retention period (all data older than this is deleted)
  retention_hours: 24

  # Periodic cleanup interval (while running)
  cleanup_interval_minutes: 60

  # Trending link threshold (keep links with this many shares)
  trending_threshold: 5

  # Cursor update interval (batch writes)
  cursor_update_seconds: 10

backfill:
  # Maximum backfill lookback (even if cursor is older)
  max_lookback_hours: 24

  # Only backfill accounts active in last N hours
  active_follows_hours: 168  # 7 days

  # Concurrent backfill workers
  concurrency: 10
```

## Benefits

1. **Predictable database size** - 24h retention keeps data bounded
2. **Fast startup** - Clean state before firehose connects
3. **Smart backfill** - Only fills gaps, never goes beyond 24h
4. **Reduced write pressure** - Batched cursor updates (every 10s vs every event)
5. **Trending link preservation** - Popular links kept regardless of age
6. **Automated maintenance** - No manual intervention needed
7. **Crash recovery** - Cursor preserved, backfill fills gap on restart

## Tradeoffs

1. **24h retention** - Loses historical data beyond 1 day
   - Mitigation: Acceptable for news aggregation use case
   - Alternative: Add archive table for long-term storage

2. **Startup time** - Cleanup and backfill add ~30-60s to startup
   - Mitigation: Worth it for clean state
   - Alternative: Run cleanup async (risky)

3. **Cursor lag** - 10s batching means up to 10s of events lost on crash
   - Mitigation: Acceptable loss for news aggregation
   - Alternative: Use write-ahead log for zero loss

## Implementation Plan

1. Add database methods (DeleteOldPosts, DeleteUnsharedLinks, etc.)
2. Implement StartupCleanup function
3. Implement SmartBackfill function
4. Add cursor batching to firehose
5. Add periodic cleanup ticker
6. Update config schema
7. Test startup sequence
8. Test crash recovery
9. Document in README

## Monitoring

Add metrics to track:

- Cleanup execution time
- Records deleted (posts, links)
- Backfill duration and records fetched
- Cursor age on startup
- Database size over time

## Related ADRs

- ADR 005: Jetstream Firehose Migration
- ADR 004: Cursor-Based Pagination
- ADR 003: Metadata Fetching Strategy

## References

- Current janitor: `cmd/janitor/main.go`
- Current backfill: `cmd/backfill/main.go`
- Cursor storage: `migrations/002_jetstream.sql`
