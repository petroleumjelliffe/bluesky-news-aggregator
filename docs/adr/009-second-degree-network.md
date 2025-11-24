# ADR 009: Second-Degree Network Support

**Status**: Proposed
**Date**: 2024-11-24

## Context

Currently, the aggregator only shows links from accounts the user directly follows (1st-degree). The most interesting content discovery often comes from "friends of friends" - accounts that multiple people you follow also follow.

## Goals

1. Collect 2nd-degree network DIDs efficiently
2. Include their posts in firehose processing
3. Surface links with appropriate weighting
4. Minimize API calls and storage

## Network Model

```
You ──follows──> Alice (1st degree) ──follows──> Charlie (2nd degree)
    ──follows──> Bob   (1st degree) ──follows──> Charlie (2nd degree)
                                    ──follows──> Diana  (2nd degree)

Charlie: source_count = 2 (both Alice and Bob follow)
Diana:   source_count = 1 (only Bob follows)
```

**Key insight**: 2nd-degree accounts followed by MULTIPLE 1st-degree accounts are more signal, less noise.

## Scale Estimation

| Metric | Conservative | Typical | Large |
|--------|--------------|---------|-------|
| Your follows (1st degree) | 100 | 300 | 1,000 |
| Avg follows per person | 200 | 500 | 1,000 |
| Raw 2nd-degree count | 20,000 | 150,000 | 1,000,000 |
| Unique after dedup | 5,000 | 30,000 | 200,000 |
| With source_count >= 2 | 500 | 5,000 | 50,000 |
| With source_count >= 3 | 100 | 1,000 | 10,000 |

**Filtering by source_count is essential** - otherwise the network explodes.

## Database Schema

```sql
-- Extend follows table or create new table
CREATE TABLE network_accounts (
    did TEXT PRIMARY KEY,
    handle TEXT,
    display_name TEXT,
    avatar_url TEXT,

    -- Network metadata
    degree INTEGER NOT NULL DEFAULT 1,  -- 1 = direct follow, 2 = friend-of-friend
    source_count INTEGER NOT NULL DEFAULT 1,  -- How many 1st-degree accounts follow this

    -- For multi-user: which user's network is this?
    -- (For single-user MVP, omit this)
    -- user_id INTEGER REFERENCES users(id),

    -- Timestamps
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    -- Track which 1st-degree accounts link to this (for debugging/display)
    -- stored as JSON array of DIDs
    source_dids JSONB DEFAULT '[]'
);

CREATE INDEX idx_network_degree ON network_accounts(degree);
CREATE INDEX idx_network_source_count ON network_accounts(source_count DESC);
CREATE INDEX idx_network_degree_count ON network_accounts(degree, source_count DESC);
```

## Collection Strategy

### Phase 1: Initial Crawl

```go
// Crawl 2nd-degree network
func (c *Crawler) Crawl2ndDegree(ctx context.Context) error {
    // Get all 1st-degree follows
    firstDegree, err := c.db.GetFollowsByDegree(1)
    if err != nil {
        return err
    }

    // Track 2nd-degree candidates with their sources
    candidates := make(map[string]*Candidate) // did -> candidate

    for _, follow := range firstDegree {
        // Rate limit: ~100 requests/minute to Bluesky API
        time.Sleep(600 * time.Millisecond)

        // Fetch who this 1st-degree account follows
        theirFollows, err := c.bluesky.GetFollows(follow.Handle)
        if err != nil {
            log.Printf("Failed to get follows for %s: %v", follow.Handle, err)
            continue
        }

        for _, f := range theirFollows {
            // Skip if already 1st-degree
            if c.isFirstDegree(f.DID) {
                continue
            }

            // Skip self
            if f.DID == c.myDID {
                continue
            }

            if existing, ok := candidates[f.DID]; ok {
                existing.SourceCount++
                existing.SourceDIDs = append(existing.SourceDIDs, follow.DID)
            } else {
                candidates[f.DID] = &Candidate{
                    DID:         f.DID,
                    Handle:      f.Handle,
                    DisplayName: f.DisplayName,
                    AvatarURL:   f.Avatar,
                    SourceCount: 1,
                    SourceDIDs:  []string{follow.DID},
                }
            }
        }

        log.Printf("Processed %s, total candidates: %d", follow.Handle, len(candidates))
    }

    // Filter and save: only keep candidates with source_count >= threshold
    threshold := 2
    for _, candidate := range candidates {
        if candidate.SourceCount >= threshold {
            c.db.UpsertNetworkAccount(candidate, 2) // degree = 2
        }
    }

    return nil
}
```

### Phase 2: Incremental Updates

```go
// Run periodically (daily/weekly) to catch new connections
func (c *Crawler) UpdateNetwork(ctx context.Context) error {
    // Option 1: Full recrawl (simple, expensive)
    // Option 2: Only check 1st-degree accounts updated recently
    // Option 3: Sample-based refresh (check 10% of network per run)

    // Start with Option 1 for MVP, optimize later
    return c.Crawl2ndDegree(ctx)
}
```

### API Rate Limiting

```go
// Bluesky API rate limits
const (
    // ~3000 requests per 5 minutes for authenticated users
    RequestsPerWindow = 3000
    WindowDuration    = 5 * time.Minute

    // Safe rate: ~10 requests/second
    MinRequestInterval = 100 * time.Millisecond
)

type RateLimiter struct {
    tokens    chan struct{}
    interval  time.Duration
}

func NewRateLimiter(rps int) *RateLimiter {
    rl := &RateLimiter{
        tokens:   make(chan struct{}, rps),
        interval: time.Second / time.Duration(rps),
    }

    // Refill tokens
    go func() {
        ticker := time.NewTicker(rl.interval)
        for range ticker.C {
            select {
            case rl.tokens <- struct{}{}:
            default:
            }
        }
    }()

    return rl
}

func (rl *RateLimiter) Wait() {
    <-rl.tokens
}
```

## Firehose Integration

### Option A: Combined Filter Set (Recommended for MVP)

```go
type DIDManager struct {
    firstDegree  map[string]bool
    secondDegree map[string]int  // did -> source_count
    mu           sync.RWMutex
}

func (dm *DIDManager) LoadFromDatabase(db *database.DB) error {
    dm.mu.Lock()
    defer dm.mu.Unlock()

    // Load 1st degree
    first, err := db.GetFollowsByDegree(1)
    if err != nil {
        return err
    }
    dm.firstDegree = make(map[string]bool, len(first))
    for _, f := range first {
        dm.firstDegree[f.DID] = true
    }

    // Load 2nd degree (only those meeting threshold)
    second, err := db.GetNetworkAccounts(2, 2) // degree=2, min_source_count=2
    if err != nil {
        return err
    }
    dm.secondDegree = make(map[string]int, len(second))
    for _, f := range second {
        dm.secondDegree[f.DID] = f.SourceCount
    }

    log.Printf("Loaded %d 1st-degree, %d 2nd-degree accounts",
        len(dm.firstDegree), len(dm.secondDegree))

    return nil
}

func (dm *DIDManager) ShouldProcess(did string) (bool, int) {
    dm.mu.RLock()
    defer dm.mu.RUnlock()

    if dm.firstDegree[did] {
        return true, 1
    }
    if sourceCount, ok := dm.secondDegree[did]; ok {
        return true, 2
    }
    return false, 0
}
```

### Option B: Tiered Processing (Future optimization)

```go
// Process 1st-degree posts immediately
// Batch 2nd-degree posts for periodic processing
func (f *Firehose) handleEvent(event *models.Event) {
    shouldProcess, degree := f.didManager.ShouldProcess(event.Did)
    if !shouldProcess {
        return
    }

    if degree == 1 {
        // Process immediately
        f.processor.ProcessEvent(event)
    } else {
        // Queue for batch processing
        f.secondDegreeQueue <- event
    }
}
```

## API Response Changes

```go
type LinkResponse struct {
    // ... existing fields ...

    // New: breakdown by network degree
    FirstDegreeShares  int `json:"first_degree_shares"`
    SecondDegreeShares int `json:"second_degree_shares"`

    // Or: weighted score
    NetworkScore float64 `json:"network_score"` // 1st-degree weighted higher
}
```

### Scoring Algorithm

```go
func CalculateNetworkScore(firstDegree, secondDegree int) float64 {
    // 1st-degree shares worth 1.0 each
    // 2nd-degree shares worth 0.3 each (diminishing value)
    return float64(firstDegree) + float64(secondDegree)*0.3
}

// Alternative: Source-count weighted
func CalculateWeightedScore(shares []Share) float64 {
    var score float64
    for _, share := range shares {
        if share.Degree == 1 {
            score += 1.0
        } else {
            // Higher source_count = more trusted 2nd-degree account
            // source_count of 5 means 5 of your follows also follow them
            weight := math.Min(float64(share.SourceCount)/5.0, 1.0) * 0.5
            score += weight
        }
    }
    return score
}
```

## UI Considerations

```
┌─────────────────────────────────────────────────────┐
│  Article Title                                      │
│  example.com                                        │
│                                                     │
│  ★ 12 shares                                       │
│  👥 3 from your follows, 9 from extended network   │
│                                                     │
│  [Avatars of sharers, 1st-degree highlighted]      │
└─────────────────────────────────────────────────────┘
```

Or filter toggle:
```
[x] My follows (1st degree)
[x] Extended network (2nd degree)
[ ] Global (everyone)
```

## Implementation Order

### Phase 1: Collection Infrastructure
1. Add `network_accounts` table (migration)
2. Implement `Crawler` with rate limiting
3. Add CLI command: `./bin/crawl-network`
4. Run initial crawl, measure results

### Phase 2: Firehose Integration
1. Update `DIDManager` to load 2nd-degree
2. Modify firehose filter to include 2nd-degree
3. Track degree in `posts` table (optional)
4. Test with real data

### Phase 3: API & UI
1. Add degree breakdown to API response
2. Update frontend to show network info
3. Add filter toggle (1st/2nd/all)

## Resource Estimates

### Initial Crawl (300 follows)

| Step | API Calls | Time (10 req/s) | Data |
|------|-----------|-----------------|------|
| Fetch follows of each | 300 × ~5 pages | 25 min | 150k follows |
| Dedupe & filter | - | seconds | ~5k accounts |
| Store | - | seconds | ~500 KB |

### Ongoing Firehose

| Scenario | DIDs in filter | Posts/hour | Storage/day |
|----------|----------------|------------|-------------|
| 1st only | 300 | ~500 | ~5 MB |
| 1st + 2nd (top 5k) | 5,300 | ~5,000 | ~50 MB |
| 1st + 2nd (top 10k) | 10,300 | ~10,000 | ~100 MB |

With 72-hour retention: 150-300 MB database for 2nd-degree support.

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| API rate limits during crawl | Spread over hours, resume on failure |
| Stale 2nd-degree data | Weekly refresh, or refresh on demand |
| Too many 2nd-degree accounts | Increase source_count threshold |
| Spam accounts in 2nd-degree | Filter by labeler (future), source_count helps |
| Slow queries with larger dataset | Indexes, consider materialized views |

## Open Questions

1. **Threshold tuning**: Start with source_count >= 2, adjust based on results?
2. **Refresh frequency**: Weekly? On-demand? Triggered by user?
3. **Display**: Show degree in UI, or just use for ranking?
4. **Multi-user**: Each user has their own 2nd-degree network (expensive) or shared pool?

## Commands

```bash
# Initial crawl
./bin/crawl-network --degree=2 --threshold=2

# Check stats
./bin/crawl-network --stats

# Refresh (incremental)
./bin/crawl-network --refresh
```

## Files to Create/Modify

1. `migrations/004_network_accounts.sql` - NEW
2. `internal/crawler/crawler.go` - NEW
3. `internal/crawler/ratelimit.go` - NEW
4. `cmd/crawl-network/main.go` - NEW
5. `internal/didmanager/manager.go` - MODIFY (add 2nd-degree support)
6. `cmd/firehose/main.go` - MODIFY (use updated DIDManager)
7. `cmd/api/main.go` - MODIFY (add degree info to response)
