# ADR 004: Cursor-based Pagination and State Management

**Status**: Accepted

**Date**: 2025-11-02

## Context

Need to efficiently poll 342 Bluesky accounts for new posts without:
- Fetching duplicate posts
- Missing posts due to high-volume accounts
- Unnecessary API calls

## Decision

Use Bluesky's cursor-based pagination with persistent state tracking.

## Cursor Format

Bluesky API returns cursors as **ISO 8601 timestamps**:
```
"cursor": "2025-08-11T23:26:32.374Z"
```

This represents the timestamp of the oldest post in the current page.

## State Machine

### Account States

1. **New Account** (no cursor in database)
   - State: `cursor = ""` or `cursor IS NULL`
   - Action: Initial ingestion (24 hours)
   - Logs: `[INITIAL]`

2. **Established Account** (has cursor)
   - State: `cursor = "2025-XX-XXTXX:XX:XX.XXXZ"`
   - Action: Incremental poll (since cursor)
   - Logs: `[POLL]` (only if new posts found)

3. **Invalid Account** (deleted/private)
   - State: API returns 400/403/404
   - Action: Skip (no retry)
   - Logs: `[SKIP]`

### State Transitions

```
             ┌─────────────┐
             │  New User   │
             │ (no cursor) │
             └──────┬──────┘
                    │
             [Initial Ingestion]
              (fetch 24h)
                    │
                    ▼
             ┌──────────────┐
             │ Has Cursor   │◄────┐
             │ in Database  │     │
             └──────┬───────┘     │
                    │              │
              [Poll for new]       │
              (since cursor)       │
                    │              │
                    ├─►[Has new posts]──►[Update cursor]──┘
                    │
                    └─►[No new posts]──►[No update needed]──┘
```

## Implementation

### Database Schema

```sql
CREATE TABLE poll_state (
    user_handle TEXT PRIMARY KEY,
    last_cursor TEXT,                 -- Timestamp from Bluesky
    last_polled_at TIMESTAMP,         -- When we last checked
    posts_fetched_count INTEGER DEFAULT 0
);
```

### Algorithm

#### Initial Ingestion

```go
func (p *Poller) pollAccountInitial(handle string) error {
    cursor := ""
    cutoffTime := time.Now().Add(-24 * time.Hour)

    for pageCount < maxPages {
        feed := fetchWithRetry(handle, cursor, 50)

        // Process posts
        for _, post := range feed.Feed {
            processPost(post)
        }

        // CRITICAL: Update cursor BEFORE checking cutoff
        if feed.Cursor != "" {
            cursor = feed.Cursor
        }

        // Check if we've reached 24h cutoff
        oldestPost := feed.Feed[len(feed.Feed)-1]
        if oldestPost.CreatedAt.Before(cutoffTime) {
            break
        }
    }

    // Save cursor for next poll
    db.UpdateCursor(handle, cursor)
}
```

**Critical Bug Fix** (2025-11-02):
- Originally: Cursor updated AFTER cutoff check
- Problem: Breaking early left cursor as empty string
- Result: Every poll did 24h ingestion
- Fix: Update cursor BEFORE checking if we should break

#### Regular Polling

```go
func (p *Poller) pollAccountRegular(handle, lastCursor string) error {
    cursor := lastCursor  // Start from saved position
    cutoffTime := time.Now().Add(-pollingInterval)

    for pageCount < 10 {  // Reasonable limit
        feed := fetchWithRetry(handle, cursor, 50)

        if len(feed.Feed) == 0 {
            break  // No new posts
        }

        // Process posts
        processPostsAndURLs(feed.Feed)

        // Update cursor before cutoff check
        if feed.Cursor != "" {
            cursor = feed.Cursor
        }

        // Gap detection: reached polling window?
        oldestPost := feed.Feed[len(feed.Feed)-1]
        if oldestPost.CreatedAt.Before(cutoffTime) {
            break  // Covered the polling window
        }
    }

    // Save new position
    db.UpdateCursor(handle, cursor)
}
```

### Gap Detection

**Problem**: High-volume accounts posting > 50 times in 15 minutes

**Solution**: Pagination with cutoff
- Fetch multiple pages if needed
- Stop when reaching polling window (15 minutes ago)
- Logs: `[POLL] handle: High volume detected, fetching more pages`

**Example**:
```
[POLL] washingtonpost.com: 150 posts, 75 URLs across 3 pages
```

## Error Handling

### Permanent Errors

**Detection**:
```go
func isPermanentError(err error) bool {
    errStr := err.Error()
    return strings.Contains(errStr, "API error: 400") ||  // Bad Request
           strings.Contains(errStr, "API error: 401") ||  // Unauthorized
           strings.Contains(errStr, "API error: 403") ||  // Forbidden
           strings.Contains(errStr, "API error: 404") ||  // Not Found
           strings.Contains(errStr, "API error: 410")     // Gone
}
```

**Handling**:
- Don't retry
- Log `[SKIP]` instead of `[ERROR]`
- Don't save cursor (keep existing state)
- Continue with next account

**Example**:
```
[SKIP] handle.invalid: Account unavailable (invalid/deleted/private): API error: 400
```

### Transient Errors

**Retry with exponential backoff**:
- Attempt 1: Immediate
- Attempt 2: +1s
- Attempt 3: +2s
- Attempt 4: +4s

**Total**: 3 retries over ~7 seconds

## Configuration

```yaml
polling:
  interval_minutes: 15           # How often to poll
  posts_per_page: 50            # Results per API call
  initial_lookback_hours: 24    # Initial ingestion window
  max_retries: 3                # Retry failed requests
  retry_backoff_ms: 1000        # Initial retry delay
  max_pages_per_user: 100       # Safety limit
```

## Performance Characteristics

### Initial Ingestion (342 accounts, no cursors)
- Duration: ~18 seconds
- API calls: ~342 calls (1 per account, most hit 24h cutoff on page 1)
- Posts fetched: ~15,000-17,000 total
- Cursors saved: 322 (95% success rate)

### Regular Polling (342 accounts, all have cursors)
- Duration: ~5-10 seconds
- API calls: ~342 calls (most return empty)
- Active accounts: ~10-30 (posting in last 15min)
- Posts fetched: ~50-150 new posts
- Logs: Only active accounts shown

### Cursor Distribution

**After first full run**:
```sql
SELECT
    COUNT(CASE WHEN last_cursor IS NOT NULL AND last_cursor != ''
               THEN 1 END) as has_cursor,
    COUNT(CASE WHEN last_cursor IS NULL OR last_cursor = ''
               THEN 1 END) as no_cursor
FROM poll_state;

-- Result: has_cursor=322, no_cursor=17
```

**No cursor cases**:
- Empty accounts (no posts in 24h)
- Accounts that reached end of feed
- Temporary API failures

## Cursor Validity

**Assumptions**:
- Cursors remain valid indefinitely
- Can reuse cursor from hours/days ago
- Bluesky API handles expired cursors gracefully

**Reality**:
- ✅ Cursors work across restarts
- ✅ Cursors work after hours of downtime
- ✅ No observed expiration

**Edge case**: Deleted posts
- If post at cursor position is deleted, API skips to next
- No special handling needed

## Database Queries

**Check if account needs initial ingestion**:
```go
cursor, err := db.GetLastCursor(handle)
if cursor == "" {
    // Do initial ingestion
} else {
    // Do regular poll
}
```

**Update cursor after successful poll**:
```go
db.UpdateCursor(handle, newCursor)
// Uses UPSERT (INSERT ... ON CONFLICT DO UPDATE)
```

## Monitoring

**Success metrics**:
- Accounts with cursors: 95%+
- Regular poll showing `[POLL]`: 5-10% of accounts
- Regular poll showing `[INITIAL]`: 0% (after first run)

**Warning signs**:
- Multiple `[INITIAL]` logs every cycle → cursor not being saved
- `[ERROR]` instead of `[SKIP]` for 400s → permanent error handling broken
- Poll duration > 30s → API issues or high volume

## Debugging

**Check cursor state**:
```sql
SELECT user_handle, last_cursor, last_polled_at
FROM poll_state
WHERE user_handle = 'example.bsky.social';
```

**View accounts without cursors**:
```sql
SELECT user_handle, last_polled_at
FROM poll_state
WHERE last_cursor IS NULL OR last_cursor = ''
ORDER BY last_polled_at DESC;
```

**Reset specific account**:
```sql
DELETE FROM poll_state WHERE user_handle = 'example.bsky.social';
-- Next poll will do initial ingestion
```

## Consequences

### Positive
- Efficient: Only fetch new posts since last poll
- Reliable: No missed posts (gap detection)
- Scalable: 342 accounts polled in ~5-10s
- Resilient: Handles errors gracefully

### Negative
- Complexity: State management adds failure modes
- Storage: Requires poll_state table
- Critical bug potential: Cursor must be saved correctly
- No backfill: Can't easily re-fetch old posts

## Related ADRs
- ADR 001: Polling Architecture
- ADR 002: Database Schema

## Bug History

**Issue**: Cursor not saved after initial ingestion
- **Symptom**: Every poll did 24h ingestion
- **Cause**: Cursor updated after break statement
- **Fix**: Move cursor update before cutoff check
- **Commit**: 512c8db
- **Date**: 2025-11-02
