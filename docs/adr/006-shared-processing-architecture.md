# ADR 006: Shared Processing Architecture

**Status**: Accepted

**Date**: 2025-11-04

## Context

The application ingests Bluesky posts from two different data sources:
1. **Jetstream (firehose)**: Real-time WebSocket stream of post events
2. **Bluesky API (backfill)**: Historical post data via REST API

Both sources provide the same conceptual data (posts with URLs and metadata) but in different formats:
- Jetstream returns thumbnails as blob references (CID) requiring CDN URL conversion
- Bluesky API returns thumbnails as direct CDN URL strings
- Field names and nesting structures differ slightly

**Problem**: Initial implementation duplicated processing logic in `cmd/firehose/` and `cmd/backfill/`, leading to:
- Code duplication (processEmbed, processURLs functions duplicated)
- Behavioral differences (firehose extracted metadata, backfill didn't)
- Difficult maintenance (fixing one didn't fix the other)
- Architectural drift over time

**Example of the problem**: Issue #17 revealed that backfill was ignoring metadata in embeds, resulting in 96% of links having no title/description/image. The firehose had been fixed earlier (Issue #14) but backfill still had the old broken code.

## Decision

**Adopt the Adapter Pattern for data ingestion with a single shared processor.**

All post processing logic lives in ONE place: `internal/processor/processor.go`. Both data sources use thin adapter layers to normalize their input format, then call the shared processor.

### Architecture

```
┌─────────────────┐         ┌─────────────────────┐
│  Jetstream      │         │  Bluesky API        │
│  (firehose)     │         │  (backfill)         │
└────────┬────────┘         └────────┬────────────┘
         │                           │
         │ Raw events                │ Raw posts
         │                           │
         ▼                           ▼
┌────────────────┐         ┌─────────────────────┐
│ Jetstream      │         │ Bluesky API         │
│ Adapter        │         │ Adapter             │
│                │         │                     │
│ - Convert      │         │ - Convert           │
│   blob → CDN   │         │   API format        │
│ - Map fields   │         │ - Map fields        │
└────────┬───────┘         └────────┬────────────┘
         │                           │
         │ processor.PostRecord      │ processor.PostRecord
         │                           │
         └───────────┬───────────────┘
                     │
                     ▼
         ┌───────────────────────┐
         │  internal/processor   │
         │                       │
         │  - ProcessEvent()     │
         │  - processEmbed()     │
         │  - processURLs()      │
         │  - Extract metadata   │
         │  - Store in database  │
         └───────────┬───────────┘
                     │
                     ▼
         ┌───────────────────────┐
         │    Database (Postgres)│
         └───────────────────────┘
```

### Key Principles

1. **Single Processing Path**: All URL extraction, metadata fetching, and database storage happens in `internal/processor/`

2. **Adapter Responsibility**: Adapters ONLY:
   - Convert external format to `processor.PostRecord`
   - Handle format-specific quirks (blob conversion, field mapping)
   - Call `processor.ProcessEvent()`
   - Do NOT implement processing logic

3. **Processor Responsibility**: The processor ONLY:
   - Extracts URLs from text, embeds, quote posts
   - Fetches/stores metadata
   - Interacts with database
   - Does NOT know about Jetstream or Bluesky API specifics

4. **Type Ownership**: Processing types live in `internal/processor/`:
   - `processor.PostRecord`
   - `processor.Embed`
   - `processor.EmbedExternal`
   - `processor.EmbedRecord`

## Implementation

### Current State (Partial Implementation)

**✅ Correct (firehose)**:
```go
// cmd/firehose/main.go
func main() {
    processor := processor.NewProcessor(db)

    for event := range jetstreamEvents {
        if err := processor.ProcessEvent(event); err != nil {
            log.Printf("Error: %v", err)
        }
    }
}
```

**⚠️ Needs Refactoring (backfill)**:
```go
// cmd/backfill/main.go
// Currently has duplicate processEmbed(), processURLs() functions
// Should be:
func main() {
    adapter := adapter.NewBlueskyAdapter(db)

    for post := range blueskyPosts {
        processorPost := adapter.Convert(post)  // Adapter converts format
        if err := adapter.Process(processorPost); err != nil {  // Shared processor
            log.Printf("Error: %v", err)
        }
    }
}
```

### Future State (Full Implementation)

**Create adapter layer**:
```go
// internal/adapter/bluesky.go
package adapter

import (
    "github.com/petroleumjelliffe/bluesky-news-aggregator/internal/bluesky"
    "github.com/petroleumjelliffe/bluesky-news-aggregator/internal/processor"
)

type BlueskyAdapter struct {
    processor *processor.Processor
}

func (a *BlueskyAdapter) ProcessPost(post *bluesky.Post) error {
    // Convert bluesky.Post → processor.PostRecord
    processorPost := &processor.PostRecord{
        Type:      post.Record.Type,
        Text:      post.Record.Text,
        CreatedAt: post.Record.CreatedAt,
        Embed:     a.convertEmbed(post.Embed),
    }

    // Build event for processor
    event := &models.Event{
        Did: post.Author.DID,
        Commit: &models.Commit{
            Collection: "app.bsky.feed.post",
            RKey:       extractRKey(post.URI),
            Operation:  "create",
            Record:     marshal(processorPost),
        },
    }

    return a.processor.ProcessEvent(event)
}

func (a *BlueskyAdapter) convertEmbed(embed *bluesky.Embed) *processor.Embed {
    if embed == nil {
        return nil
    }

    return &processor.Embed{
        Type:     embed.Type,
        External: a.convertExternal(embed.External),
        Record:   a.convertRecord(embed.Record),
    }
}
```

## Architectural Rules

### ✅ DO

1. **Add new processing features in `internal/processor/`**
   ```go
   // internal/processor/processor.go
   func (p *Processor) processImages(post *PostRecord) {
       // New feature: extract images from posts
   }
   ```

2. **Use adapters for format conversion**
   ```go
   // internal/adapter/bluesky.go
   func (a *BlueskyAdapter) convertThumb(thumb string) string {
       // Convert Bluesky API thumb format if needed
       return thumb
   }
   ```

3. **Keep cmd/ files thin (main loop only)**
   ```go
   // cmd/backfill/main.go
   func main() {
       adapter := adapter.NewBlueskyAdapter(db)
       for post := range fetchPosts() {
           adapter.ProcessPost(post)
       }
   }
   ```

4. **Reference ADRs in code for architectural decisions**
   ```go
   // See ADR 006: Shared Processing Architecture
   processor := processor.NewProcessor(db)
   ```

### ❌ DO NOT

1. **Create processing logic in cmd/ directories**
   ```go
   // ❌ cmd/backfill/main.go
   func processEmbed(embed *bluesky.Embed) {
       // WRONG: This is processing logic, belongs in internal/processor/
   }
   ```

2. **Duplicate processEmbed(), processURLs(), etc.**
   ```go
   // ❌ cmd/firehose/main.go
   func extractURLs(text string) []string {
       // WRONG: Already exists in internal/urlutil/
   }
   ```

3. **Define processing types outside internal/processor/**
   ```go
   // ❌ cmd/backfill/types.go
   type PostRecord struct {  // WRONG: Use processor.PostRecord
       Text string
   }
   ```

4. **Mix format conversion with processing logic**
   ```go
   // ❌ internal/processor/processor.go
   func (p *Processor) processBlobThumb(cid string, authorDID string) {
       // WRONG: Blob conversion is format-specific, belongs in adapter
   }
   ```

## Examples

### Example 1: Adding New URL Extraction Feature

**❌ INCORRECT**:
```go
// cmd/backfill/main.go
func extractYouTubeIDs(text string) []string {
    // Duplicating logic in multiple places
}

// cmd/firehose/main.go
func extractYouTubeIDs(text string) []string {
    // Same function, duplicated
}
```

**✅ CORRECT**:
```go
// internal/processor/processor.go
func (p *Processor) processVideoEmbeds(post *PostRecord) {
    youtubeIDs := extractYouTubeIDs(post.Text)
    for _, id := range youtubeIDs {
        p.db.StoreVideo(id)
    }
}

// Both cmd/firehose and cmd/backfill automatically benefit
```

### Example 2: Handling Format Differences

**❌ INCORRECT**:
```go
// internal/processor/processor.go
func (p *Processor) processEmbed(embed *Embed, source string) {
    if source == "jetstream" {
        // Handle blob conversion
    } else if source == "bluesky" {
        // Handle direct URL
    }
    // WRONG: Processor shouldn't know about sources
}
```

**✅ CORRECT**:
```go
// internal/adapter/jetstream.go
func (a *JetstreamAdapter) convertThumb(thumb interface{}, authorDID string) string {
    // Handle blob → CDN conversion
    if blob, ok := thumb.(map[string]interface{}); ok {
        cid := extractCID(blob)
        return fmt.Sprintf("https://cdn.bsky.app/img/feed_thumbnail/plain/%s/%s@jpeg",
            authorDID, cid)
    }
    return thumb.(string)
}

// internal/adapter/bluesky.go
func (a *BlueskyAdapter) convertThumb(thumb string) string {
    // Already a URL, no conversion needed
    return thumb
}

// internal/processor/processor.go
func (p *Processor) processEmbed(embed *Embed) {
    // Just uses thumb as a string URL, doesn't care about source
    if embed.External != nil {
        p.storeMetadata(embed.External.Thumb)
    }
}
```

## Verification

Before committing code, verify:

1. **Run `/check-duplicates`**: Search for duplicate functions
2. **Run `/check-architecture`**: Verify processor usage
3. **Check import paths**:
   - `cmd/firehose` should import `internal/processor`
   - `cmd/backfill` should import `internal/adapter` and `internal/processor`
   - Neither should duplicate processing logic

4. **Test both data sources**:
   ```bash
   # Test firehose
   ./bin/firehose

   # Test backfill
   ./bin/backfill

   # Verify database output is identical for same posts
   psql -d bluesky_news -c "SELECT * FROM links WHERE metadata_fetched_at IS NOT NULL"
   ```

## Consequences

### Positive

1. **DRY Principle**: Processing logic exists in one place
2. **Consistency**: Both data sources behave identically
3. **Easier Maintenance**: Fix bugs once, both sources benefit
4. **Clear Boundaries**: Adapter vs processor responsibilities are explicit
5. **Testability**: Can test processor independently of data sources
6. **Extensibility**: Adding new data sources requires only new adapter

### Negative

1. **Additional Abstraction**: Adapter layer adds complexity
2. **Type Conversion Overhead**: Small performance cost for mapping
3. **Learning Curve**: Developers must understand adapter pattern

### Neutral

1. **Migration Required**: Backfill needs refactoring (see Issue #20)
2. **Type Definitions**: Need canonical types package (see Issue #21)

## Related

- **ADR 003**: Metadata Fetching Strategy (explains why metadata extraction is critical)
- **ADR 005**: Jetstream Firehose Migration (background on dual data sources)
- **Issue #17**: Backfill metadata bug that revealed architecture problems
- **Issue #20**: Refactor backfill to use shared processor (implementation work)
- **Issue #21**: Create canonical types package (supporting work)
- **Issue #22**: Add architecture documentation and diagrams (documentation work)

## Compliance

### Project Instructions

See [.claude/project_instructions.md](.claude/project_instructions.md) for:
- Architecture diagram
- Red flags that violate this ADR
- Checkpoint protocol
- Refactoring checklist

### Slash Commands

Before coding:
- `/check-duplicates` - Verify no duplicate processing functions
- `/check-architecture` - Verify processor usage
- `/pre-commit` - Comprehensive checklist

### Code Comments

`internal/processor/processor.go` has architectural warnings:
```go
// ⚠️ ARCHITECTURAL WARNING ⚠️
// This processor is the ONLY place where post/URL/metadata processing should occur.
// Both cmd/firehose (Jetstream) and cmd/backfill (Bluesky API) MUST use this processor.
```

## Review Checklist

When reviewing code changes:

- [ ] Processing logic only in `internal/processor/`?
- [ ] Format conversion only in `internal/adapter/` or cmd/ adapters?
- [ ] No duplicate function names (processEmbed, processURLs, etc.)?
- [ ] Both firehose and backfill tested?
- [ ] Types defined in correct package?
- [ ] ADR referenced in code comments if architectural?

## Future Work

1. **Phase 1** (Issue #20): Refactor backfill to use shared processor
   - Create `internal/adapter/bluesky.go`
   - Remove duplicate processing functions from `cmd/backfill/main.go`
   - Add integration tests

2. **Phase 2** (Issue #21): Create canonical types package
   - Move common types to `internal/types/`
   - Update imports across codebase
   - Document type ownership

3. **Phase 3** (Issue #22): Documentation improvements
   - Add architecture diagram to README
   - Create CONTRIBUTING.md with patterns
   - Link code to ADRs with comments
