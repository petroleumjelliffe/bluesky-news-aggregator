---
description: Verify changes follow architecture principles
---

# Architecture Compliance Check

## Task
Verify that current changes or planned changes follow the project's architectural principles.

## Architecture Rules

### Rule 1: Single Processing Path
Both firehose and backfill MUST use `internal/processor/` for post processing.

```
✅ CORRECT:
cmd/firehose → internal/processor → database
cmd/backfill → internal/processor → database

❌ WRONG:
cmd/firehose → internal/processor → database
cmd/backfill → custom logic in main.go → database
```

### Rule 2: No Processing Logic in cmd/
Command directories (`cmd/*`) should only:
- Parse CLI arguments
- Initialize dependencies
- Call shared libraries
- Handle service lifecycle

They should NOT:
- Process posts/embeds/URLs
- Extract metadata
- Define processing types

### Rule 3: Adapter Pattern
Different data sources use adapters to convert to canonical types:

```
Jetstream → Adapter → Processor
Bluesky API → Adapter → Processor
```

### Rule 4: Type Definitions
- Canonical types belong in `internal/types/` (or processor until refactored)
- No duplicate type definitions across packages
- External types (Jetstream, Bluesky API) stay in their packages

## Verification Steps

1. **Check processor usage**
   ```
   Search cmd/firehose/main.go for:
   - import "internal/processor" ✅
   - calls to processor methods ✅
   - NO process* functions defined ✅

   Search cmd/backfill/main.go for:
   - import "internal/processor" ✅
   - calls to processor methods ✅
   - NO process* functions defined ✅
   ```

2. **Check for processing logic in cmd/**
   ```
   Search cmd/ for these patterns:
   - func.*process.*\(
   - func.*extract.*\(
   - func.*handle.*Embed
   - type.*Record
   - type.*Embed

   If found → VIOLATION
   ```

3. **Verify shared types**
   ```
   Check for duplicate type definitions:
   - internal/processor/processor.go: PostRecord, Embed
   - internal/bluesky/types.go: Post, Embed
   - cmd/*/: Should have NONE

   If types duplicated in cmd/ → VIOLATION
   ```

4. **Check import patterns**
   ```
   cmd/firehose should import:
   - internal/processor ✅
   - internal/jetstream ✅
   - internal/database ✅

   cmd/backfill should import:
   - internal/processor ✅
   - internal/bluesky ✅
   - internal/database ✅

   Both should NOT import each other ✅
   ```

## Report Format

### If Compliant
```
✅ Architecture Check PASSED

Verified:
- Both firehose and backfill use internal/processor
- No processing logic in cmd/ directories
- No duplicate type definitions
- Correct import patterns

Safe to proceed.
```

### If Violations Found
```
❌ Architecture Check FAILED

Violations:
1. [cmd/backfill/main.go:350] - processEmbed() defined in cmd/
   → Should be in internal/processor

2. [cmd/firehose/main.go:120] - PostRecord type defined
   → Should use processor types

Recommendations:
- Move processing functions to internal/processor
- Remove duplicate type definitions
- Update imports to use shared processor

STOP and fix violations before proceeding.
```

## Common Violations

### Violation: Processing in cmd/
```go
// cmd/backfill/main.go
func (b *Backfiller) processEmbed(...) {  // ❌ WRONG
    // processing logic
}
```

**Fix**: Remove from cmd/, use processor
```go
// cmd/backfill/main.go
import "internal/processor"

func (b *Backfiller) handlePost(post *bluesky.Post) {
    // Convert to processor types (adapter)
    processorPost := adaptBlueskyPost(post)
    // Use processor
    b.processor.ProcessPost(processorPost)  // ✅ CORRECT
}
```

### Violation: Duplicate types
```go
// cmd/backfill/main.go
type Embed struct {  // ❌ WRONG - duplicates processor.Embed
    External *EmbedExternal
}
```

**Fix**: Use processor types
```go
// cmd/backfill/main.go
import "internal/processor"

// Use processor.Embed, processor.EmbedExternal  // ✅ CORRECT
```

## Related ADRs
- ADR 005: Jetstream Firehose Migration
- ADR 006: Shared Processing Architecture (future)
- ADR 003: Metadata Fetching Strategy
