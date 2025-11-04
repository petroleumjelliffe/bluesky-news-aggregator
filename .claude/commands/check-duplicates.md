---
description: Search for duplicate functions and suggest refactoring
---

# Check for Code Duplication

## Task
Search codebase for duplicate functions before proceeding with implementation.

## Steps

1. **Ask user what they're implementing**
   - "What functionality are you adding/modifying?"
   - Get function name or description

2. **Search for similar function names**
   ```
   Use Grep tool to search for:
   - Exact function name
   - Similar patterns (process*, handle*, extract*, fetch*)
   - Related keywords from description
   ```

3. **Check critical locations**
   - `internal/processor/` - Should contain ALL processing logic
   - `cmd/firehose/main.go` - Should NOT have process* functions
   - `cmd/backfill/main.go` - Should NOT have process* functions
   - Look for duplicate type definitions

4. **Verify shared code usage**
   - Check if firehose imports `internal/processor`
   - Check if backfill imports `internal/processor`
   - Verify both use the same processor methods

5. **Report findings**

   **If NO duplicates found**:
   ```
   ✅ No duplicate code detected
   ✅ Safe to proceed with implementation
   ```

   **If duplicates found**:
   ```
   ⚠️ WARNING: Found similar code

   Locations:
   - [file:line] - [function name]
   - [file:line] - [function name]

   Recommendation:
   A) Reuse existing code in [location] (RECOMMENDED)
   B) Refactor to shared package (if in different packages)
   C) Proceed with duplication (requires justification)

   How should I proceed?
   ```

6. **Wait for user decision** before continuing

## Red Flags

If found:
- `processEmbed()` in cmd/firehose AND cmd/backfill
- `processURLs()` in cmd/firehose AND cmd/backfill
- Similar type definitions in multiple places
- Processing logic outside `internal/processor/`

→ **STOP and recommend refactoring**

## Example Usage

User: "I need to add support for video embeds"

Assistant runs /check-duplicates:
1. Searches for: `processEmbed`, `embed`, `video`
2. Finds: `processEmbed()` in `internal/processor/processor.go:167`
3. Reports: "✅ Found existing embed handler in processor. Recommend adding video support there instead of creating new function."
