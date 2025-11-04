---
description: Run comprehensive checks before creating commits or PRs
---

# Pre-Commit Checklist

Run this before `git commit` or creating a pull request.

## Critical Checks

### 1. Code Duplication
- [ ] Searched for duplicate functions using `/check-duplicates`
- [ ] Verified no similar code exists in other packages
- [ ] If duplicates found: Refactored to shared code OR justified why different

### 2. Architecture Compliance
- [ ] Ran `/check-architecture` - all checks passed
- [ ] Processing logic is in `internal/processor`, NOT `cmd/`
- [ ] No duplicate type definitions
- [ ] Both firehose and backfill use same processor (if applicable)

### 3. Shared Code Verification
- [ ] If modified processor: Tested with BOTH firehose AND backfill
- [ ] If modified backfill: Checked if firehose needs same change
- [ ] If modified firehose: Checked if backfill needs same change
- [ ] Verified both data sources produce identical database output

### 4. Metadata Extraction
If changes affect URL/metadata extraction:
- [ ] Blob → CDN URL conversion working (firehose)
- [ ] API thumbnail URLs working (backfill)
- [ ] External embed metadata extracted
- [ ] Quote post URLs extracted
- [ ] Text URLs extracted

### 5. Testing
- [ ] Manually tested the change
- [ ] Verified no regressions
- [ ] Checked database for expected data
- [ ] Reviewed logs for errors

### 6. Documentation
- [ ] Updated ADR if architecture changed
- [ ] Added code comments for complex logic
- [ ] Referenced ADR numbers in code if architectural decision
- [ ] Updated README if user-facing change

## Automated Checks

Run these commands:

```bash
# Build check
go build ./...

# Test check (if tests exist)
go test ./...

# Vet check
go vet ./...
```

## Review Questions

Answer these before committing:

**Q: Does this change affect both firehose and backfill?**
- If YES: Did you update both? Or is change in shared processor?
- If UNSURE: Check if both use this code path

**Q: Did you create any new `process*` functions?**
- If YES in cmd/: ❌ STOP - Move to internal/processor
- If YES in internal/processor: ✅ OK
- If NO: ✅ OK

**Q: Did you add new types?**
- If YES: Are they in the right place? (processor, not cmd/)
- If duplicate existing types: ❌ STOP - Use existing types

**Q: Will this break existing functionality?**
- If UNSURE: Test with both data sources

## Git Commit Message Format

```
<type>: <subject>

<body>

<footer>
```

**Types**:
- `feat`: New feature
- `fix`: Bug fix
- `refactor`: Code restructuring
- `docs`: Documentation
- `test`: Tests
- `chore`: Maintenance

**Example**:
```
fix: Extract metadata from backfill embeds

Previously backfill only extracted URLs from post text, ignoring
metadata in embed.external (title, description, thumb). This caused
96% of links to have no metadata.

Now backfill uses same metadata extraction as firehose via shared
processor, resulting in 96% metadata coverage.

Fixes #17
Related: ADR 003, ADR 006
```

## Final Verification

Before running `git commit`:

```
⚠️ STOP - Answer these:

1. Did I run /check-duplicates? YES / NO
2. Did I run /check-architecture? YES / NO
3. Did I test with both data sources (if applicable)? YES / NO / N/A
4. Did I update ADRs (if architectural)? YES / NO / N/A
5. Is this change in the right layer (cmd vs internal)? YES / NO

If ANY answer is NO (and not N/A): FIX IT FIRST
```

## Common Mistakes to Catch

🚨 **Stop if you see**:
- New `processEmbed()` in cmd/backfill
- New `processURLs()` in cmd/firehose
- New type definitions duplicating processor types
- Changes to embed handling in only ONE of firehose/backfill
- No ADR update despite architectural change

## After Commit

If committing to a PR branch:
- [ ] PR title describes the change
- [ ] PR body includes testing notes
- [ ] PR references related issues/ADRs
- [ ] Ready for review

## Related Commands
- `/check-duplicates` - Find duplicate code
- `/check-architecture` - Verify architecture compliance
