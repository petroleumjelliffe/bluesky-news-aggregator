# ADR 003: Metadata Fetching Strategy

**Status**: Accepted

**Date**: 2025-11-02

## Context

Need to display rich link previews (title, description, image) for shared URLs. Two sources available:
1. Bluesky's pre-fetched metadata (in `embed.External`)
2. Direct scraping of the URL

## Decision

**Hybrid approach**: Prefer Bluesky's metadata, fallback to scraping only when necessary.

## Implementation

### Decision Tree

```
Post contains URL
├─ Has embed.External with title?
│  ├─ YES → Use Bluesky metadata (fast path)
│  │         - Title: embed.External.Title
│  │         - Description: embed.External.Description
│  │         - Image: embed.External.Thumb
│  │         - Store in database
│  └─ NO → Scrape URL (slow path)
│            - Extract domain for rate limiting
│            - Fetch HTML via HTTP client
│            - Parse OpenGraph tags
│            - Store in database
```

### Code Location

`cmd/poller/main.go:processEmbed()`

```go
if embed.External != nil {
    if embed.External.Title != "" {
        // Fast path: Use Bluesky's metadata
        processExternalWithMetadata(...)
    } else {
        // Slow path: Scrape URL
        processURLs(...)
    }
}
```

## Metadata Sources

### 1. Bluesky Pre-fetched (Preferred)

**When available**:
- User posts link with rich embed/card
- Bluesky client fetched metadata when post was created
- `$type: "app.bsky.embed.external"`

**Advantages**:
- Instant (no HTTP request needed)
- No rate limiting concerns
- No scraping errors
- Metadata already validated by Bluesky

**Coverage**: ~96% of links (based on testing)

**Example**:
```json
{
  "embed": {
    "$type": "app.bsky.embed.external",
    "external": {
      "uri": "https://example.com/article",
      "title": "Article Title",
      "description": "Article description...",
      "thumb": "https://cdn.bsky.app/img/..."
    }
  }
}
```

### 2. Direct Scraping (Fallback)

**When needed**:
- Plain text URL in post (no rich embed)
- Bluesky didn't fetch metadata (rare)
- Quote posts with URLs in text

**Advantages**:
- Guaranteed to attempt fetching
- Can get metadata Bluesky missed

**Disadvantages**:
- HTTP request overhead (~500ms-5s per URL)
- Rate limiting required
- Anti-bot protections (403 errors)
- Timeout issues
- Retry complexity

**Coverage**: ~4% of links

## Scraper Design

### HTTP Client Configuration

**Two clients**:
1. Default: HTTP/2 with TLS 1.2+
2. Fallback: HTTP/1.1 only (for HTTP/2 INTERNAL_ERROR)

**Timeouts**: 10 seconds per request

**Headers** (browser-like to avoid bot detection):
```
User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) ...
Accept: text/html,application/xhtml+xml,application/xml;q=0.9,...
Accept-Language: en-US,en;q=0.9
Accept-Encoding: gzip, deflate, br
Cache-Control: no-cache
```

### Rate Limiting

**Per-Domain Rate Limiter**:
- Minimum 1 second between requests to same domain
- Thread-safe (mutex-based)
- Prevents rate limit errors (429)
- Tracks last request time per domain

```go
type DomainRateLimiter struct {
    lastRequest map[string]time.Time
    mu          sync.RWMutex
    minDelay    time.Duration  // 1 second
}
```

### Retry Logic

**Strategy**: Exponential backoff
- Max retries: 2
- Delays: 500ms, 1s

**Retryable errors**:
- Timeout
- Connection reset/refused
- EOF
- 502, 503, 504 (server errors)

**Non-retryable (permanent) errors**:
- 400, 401, 403, 404, 410 (client errors)

### HTML Parsing

**Library**: `github.com/PuerkitoBio/goquery`

**Extraction order**:
1. OpenGraph tags (`og:title`, `og:description`, `og:image`)
2. Fallback to standard HTML (`<title>`, `<meta name="description">`)
3. Twitter card fallback (`twitter:image`)

**Body size limit**: 1MB (prevents reading huge files)

## Performance Impact

### Before Optimization (Issue #14)
- **All URLs scraped**: 100% of links
- **Errors per 30s test**: 409 errors
- **Top errors**:
  - 200 × 403 Forbidden (anti-bot)
  - 112 × HTTP/2 INTERNAL_ERROR
  - 43 × 429 Rate Limited

### After Optimization (Current)
- **Bluesky metadata**: 96% of links
- **Scraped**: 4% of links (plain text URLs only)
- **Errors per 30s test**: 14 errors (96% reduction!)
- **Remaining errors**: Mostly timeouts from plain text URLs

### Metrics

**Per 15-minute cycle** (~50-150 new links):
- Bluesky metadata: ~48-144 links (instant)
- Scraping needed: ~2-6 links (~2-30 seconds)

## Error Handling

### Common Issues

**Washington Post timeouts**:
```
Error fetching OG data for https://wapo.st/XXX:
context deadline exceeded (Client.Timeout exceeded)
```
- Cause: WaPo servers slow/blocking automated requests
- Impact: Metadata not stored, link shows without preview
- Mitigation: HTTP/1.1 fallback, retry logic

**Anti-bot 403s**:
```
Error fetching OG data for https://patreon.com/...: status code: 403
```
- Cause: Site blocks automated requests
- Impact: No retry (permanent error)
- Mitigation: None currently (would need browser automation)

### Logging

- Scraper errors logged but non-fatal
- Link still stored in database (without metadata)
- Application continues processing

## Alternative Approaches Considered

### 1. Always scrape (rejected)
**Pros**: Complete control, get latest metadata
**Cons**: 96% more HTTP requests, severe rate limiting, errors

### 2. Use third-party service (Embedly, Iframely)
**Pros**: Reliable, handles anti-bot, cached metadata
**Cons**: Cost, external dependency, API limits
**Status**: Created Issue #10 for future investigation

### 3. HTTP HEAD requests only
**Pros**: Faster than GET
**Cons**: Many sites don't return OpenGraph in HEAD, limited benefit
**Status**: Not pursued

## Code Locations

- **Main logic**: `cmd/poller/main.go:processEmbed()`
- **Scraper**: `internal/scraper/scraper.go`
- **URL normalization**: `internal/urlutil/`

## Consequences

### Positive
- 96% reduction in HTTP requests
- 96% reduction in scraping errors
- Faster processing (no wait for HTTP)
- Lower rate limiting risk
- Better reliability

### Negative
- Still have timeout issues for plain text URLs
- No solution for anti-bot sites (403s)
- Dependent on Bluesky's metadata quality
- No metadata refresh strategy

## Related Issues
- Issue #2: Washington Post HTTP/2 errors (partially mitigated)
- Issue #4: Rate limiting errors (resolved via rate limiter)
- Issue #10: Investigate third-party preview services (future)
- Issue #14: Use Bluesky metadata (implemented)

## Future Improvements
1. Implement LinkPreview.net or Iframely for difficult domains
2. Add metadata freshness TTL and re-fetch logic
3. Better handling of URL shorteners (buff.ly, bit.ly)
4. Parallel scraping with connection pooling

## Lessons Learned

### Issue #17: Backfill Metadata Extraction Bug (2025-11-04)

**Problem**: After fresh database migration and backfill, 96% of links (2,585 out of 2,586) had no metadata (null title, description, image_url) despite Bluesky providing this data in embed objects.

**Root Cause**: Code duplication and architectural drift
- `cmd/firehose/main.go` used shared `internal/processor/processor.go` (correct)
- `cmd/backfill/main.go` had duplicate `processEmbed()` function (incorrect)
- Backfill's `processEmbed()` only extracted URLs, ignoring metadata
- When firehose was fixed in Issue #14, backfill was not updated

**Technical Details**:
```go
// ❌ INCORRECT (backfill before fix)
func processEmbed(embed *bluesky.Embed) {
    if embed.External != nil {
        urls := []string{embed.External.URI}
        processURLs(urls)  // Only stores URL, ignores Title/Description/Thumb
    }
}

// ✅ CORRECT (backfill after fix)
func processEmbed(embed *bluesky.Embed) {
    if embed.External != nil {
        if embed.External.Title != "" {
            // Use pre-fetched metadata from Bluesky
            processExternalWithMetadata(
                embed.External.URI,
                embed.External.Title,
                embed.External.Description,
                embed.External.Thumb,
            )
        }
    }
}
```

**Data Source Differences**:
- **Jetstream (firehose)**: Returns `Thumb` as blob reference that needs CDN URL conversion
- **Bluesky API (backfill)**: Returns `Thumb` as direct CDN URL string
- Both provide pre-fetched metadata in `embed.External`

**Resolution**:
1. Added `processExternalWithMetadata()` to backfill
2. Updated backfill's `processEmbed()` to extract metadata
3. Result: 2,530 out of 2,634 links (96%) now have metadata
4. Pull Request: #19

**Prevention Measures Implemented**:
1. **Documentation**: Created `.claude/project_instructions.md` with:
   - Explicit architecture rules (single processing path)
   - Red flags that trigger stop-and-ask behavior
   - Checkpoint protocol for reminders
   - Case study of this bug

2. **Slash Commands**: Created verification tools:
   - `/check-duplicates` - Search for duplicate functions before coding
   - `/check-architecture` - Verify both data sources use processor
   - `/pre-commit` - Comprehensive checklist before commits

3. **Code Comments**: Added architectural warnings to `internal/processor/processor.go`:
   ```go
   // ⚠️ ARCHITECTURAL WARNING ⚠️
   // This processor is the ONLY place where post/URL/metadata processing should occur.
   // Both cmd/firehose (Jetstream) and cmd/backfill (Bluesky API) MUST use this processor.
   ```

4. **ADR Updates**:
   - This section in ADR 003
   - New ADR 006 documenting shared processing architecture

**Related Work**:
- Issue #20: Refactor backfill to fully use shared processor (planned)
- Issue #21: Create canonical types package (planned)
- Issue #22: Add architecture documentation and diagrams (planned)
- ADR 006: Shared Processing Architecture (to be created)

**Key Takeaway**: When two data sources produce the same output (posts with links), they MUST use the same processing logic. Differences in input format should be handled by thin adapter layers, not by duplicating core logic.
