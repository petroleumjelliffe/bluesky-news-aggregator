# Bluesky News Aggregator - Development Guidelines

## CRITICAL: Check Before Coding

Before writing ANY code that processes posts/links/metadata, you MUST:

1. **Check for existing implementations**:
   - Search for similar function names: `processEmbed`, `processURLs`, `processPost`
   - Check `internal/processor/` for shared logic
   - Use `Grep` tool: Search for the function name across the codebase

2. **Ask user for approval if duplicating logic**:
   - STOP and ask: "I found similar code in X. Should I reuse it or is this intentionally different?"
   - Explain the trade-offs
   - Wait for explicit approval before proceeding

3. **Verify shared code is actually shared**:
   - If firehose uses `internal/processor`, backfill MUST also use it
   - Check imports in both `cmd/firehose/main.go` and `cmd/backfill/main.go`
   - They should both import `internal/processor`

## Architecture Principles

### Single Source of Truth

```
┌─────────────┐  ┌─────────────┐
│  Firehose   │  │  Backfill   │
└──────┬──────┘  └──────┬──────┘
       │                │
       ▼                ▼
┌─────────────┐  ┌─────────────┐
│  Adapter    │  │  Adapter    │
│ (Jetstream) │  │ (Bluesky)   │
└──────┬──────┘  └──────┬──────┘
       │                │
       └────────┬───────┘
                ▼
        ┌───────────────┐
        │   Processor   │ ← SINGLE processing path
        │   (Shared)    │ ← internal/processor/
        └───────┬───────┘
                ▼
        ┌───────────────┐
        │   Database    │
        └───────────────┘
```

**Rule**: Data processing belongs in `internal/processor/` ONLY

- **Post/URL extraction**: Use processor, not custom logic in cmd/
- **Metadata fetching**: Use shared `internal/scraper/`
- **Type definitions**: Use canonical types from `internal/types/` (future)

### Adapter Pattern

```
External Source → Adapter → Processor → Database
```

- **Jetstream**: Adapter converts Jetstream events → processor types
- **Bluesky API**: Adapter converts API responses → processor types
- **Never**: Duplicate processing logic in cmd/ directories

## Red Flags (Stop and Ask User)

🚨 **STOP if you're about to**:
- Create a function named `process*` in `cmd/` directory
- Copy-paste code between `cmd/firehose` and `cmd/backfill`
- Define types that look similar to `internal/processor` types
- Extract URLs or metadata without using `internal/processor`
- Implement embed handling outside of `internal/processor`

✋ **Instead**:
1. Stop coding
2. Tell user: "⚠️ WAIT: I'm about to [action]. I found similar code in [location]. Should I proceed or refactor?"
3. Present options:
   - A) Reuse existing code (recommended)
   - B) Refactor to shared package
   - C) Duplicate (if truly different use case - needs justification)
4. Wait for response

## Workflow: Checkpoint Protocol

### When I Should Pause and Ask

If ANY of these conditions are true, I will STOP and ask you:

1. **About to duplicate code**
   - "⚠️ WAIT: I found similar code in [location]. Proceed with duplication or refactor?"

2. **About to skip a step**
   - "⚠️ WAIT: I'm about to skip checking [X]. Continue or check first?"

3. **Architecture decision needed**
   - "⚠️ WAIT: This could go in [A] or [B]. Which approach?"

4. **Multiple files need same change**
   - "⚠️ WAIT: Found [N] files that may need this change. Update all or just this one?"

5. **Uncertainty about impact**
   - "⚠️ WAIT: This change might affect [systems]. Should I investigate first?"

### Your Reminder Phrase

If you notice I'm about to skip a step, say:
> "CHECKPOINT: Did you check [X]?"

I will immediately:
1. Stop current task
2. Check [X]
3. Report findings
4. Ask how to proceed

### My Reminder Phrase

If I notice we're about to skip a step, I'll say:
> "⚠️ WAIT: About to skip [X]. Proceed or check first?"

You respond:
- "CHECK FIRST" → I'll complete the step
- "PROCEED" → I'll skip and document why

## Refactoring Checklist

When adding a feature to firehose OR backfill:

- [ ] Does similar code exist in the other?
- [ ] Should this be in `internal/processor`?
- [ ] Do both firehose and backfill need this?
- [ ] Are we creating duplicate types?
- [ ] Did I check for existing functions with Grep?

If YES to any: **Stop and ask user before proceeding**

## Testing Requirements

When modifying processor OR adapters:
- [ ] Test with Jetstream input (firehose)
- [ ] Test with Bluesky API input (backfill)
- [ ] Verify both produce identical database entries
- [ ] Check metadata extraction works in both paths
- [ ] Verify blob → CDN URL conversion (firehose only)

## ADR Compliance

Before major changes:
1. Check if an ADR exists for this area (`docs/adr/`)
2. If it does: Follow it or propose update
3. If it doesn't: Create one if change affects architecture
4. Reference ADR number in code comments for architectural decisions

## Common Patterns

### Adding a new metadata field

1. Add field to `internal/processor` types
2. Update `processEmbed()` or `processExternalWithMetadata()` in processor
3. Update database schema if needed
4. **Do NOT** add separate logic to cmd/backfill or cmd/firehose
5. Test with both data sources

### Handling a new embed type

1. Update `internal/processor/processor.go` Embed types
2. Add handling in `processEmbed()`
3. Add adapter logic if format differs between sources
4. Test with both Jetstream and API data

## File Organization

```
cmd/
├── firehose/      # Jetstream consumer (uses processor)
├── backfill/      # API polling (uses processor)
├── api/           # REST API server
└── ...

internal/
├── processor/     # SINGLE processing pipeline ⭐
├── scraper/       # Metadata fetching
├── database/      # DB operations
├── bluesky/       # API client
├── jetstream/     # Jetstream client
└── types/         # Canonical types (future)
```

**Golden Rule**: If it processes posts, it belongs in `internal/processor/`

## Reference

- **ADRs**: `docs/adr/`
- **Architecture**: `docs/adr/005-jetstream-firehose-migration.md`
- **Shared processor**: `internal/processor/processor.go`
- **Metadata strategy**: `docs/adr/003-metadata-fetching-strategy.md`

## Slash Commands

Use these commands to check compliance:

- `/check-duplicates` - Search for duplicate functions before coding
- `/check-architecture` - Verify changes follow architecture
- `/pre-commit` - Run all checks before committing

## Previous Issues to Avoid

### Issue: Backfill metadata extraction bug (Nov 2025)

**What happened**: Backfill had its own `processEmbed()` that only extracted URLs, not metadata. Firehose used shared processor that extracted metadata. Result: 96% of links missing metadata.

**Root cause**: Code duplication - backfill didn't use shared processor

**Prevention**:
- ✅ Always check if processor already handles this
- ✅ Use Grep to find similar function names
- ✅ Both firehose and backfill must use `internal/processor`
- ✅ Integration tests that verify both sources produce same output

### Issue: Blob reference handling (Nov 2025)

**What happened**: Jetstream returns thumbnail as blob reference, API returns as URL. Firehose had conversion logic, backfill didn't need it.

**Root cause**: Data source differences not abstracted

**Prevention**:
- ✅ Use adapter pattern to normalize inputs
- ✅ Processor works with canonical types
- ✅ Document format differences in ADRs
