# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) documenting key architectural decisions for the Bluesky News Aggregator project.

## What is an ADR?

An Architecture Decision Record captures an important architectural decision made along with its context and consequences. They help teams understand:
- Why certain decisions were made
- What alternatives were considered
- What trade-offs were accepted
- What the current state of the system is

## Index

### Current Architecture (Polling-based)

1. [**ADR 001: Polling Architecture**](001-polling-architecture.md)
   - Polling-based data ingestion using Bluesky AT Protocol APIs
   - 15-minute polling interval
   - Concurrent processing with rate limiting
   - **Status**: Accepted (to be superseded by Jetstream relay)

2. [**ADR 002: Database Schema**](002-database-schema.md)
   - PostgreSQL schema design
   - Tables: posts, links, post_links, poll_state
   - Normalized URL storage and deduplication
   - **Status**: Accepted

3. [**ADR 003: Metadata Fetching Strategy**](003-metadata-fetching-strategy.md)
   - Hybrid approach: Prefer Bluesky metadata, fallback to scraping
   - 96% reduction in HTTP requests
   - Per-domain rate limiting and retry logic
   - **Status**: Accepted

4. [**ADR 004: Cursor-based Pagination**](004-cursor-based-pagination.md)
   - State management using cursors
   - Initial vs. regular polling modes
   - Gap detection for high-volume accounts
   - **Status**: Accepted

## Quick Reference

### System Overview

**Current Architecture**: Polling-based
- **Components**: Poller, Bluesky Client, Scraper, PostgreSQL database
- **Scale**: 342 accounts, ~5-10s per cycle, 15-minute intervals
- **Language**: Go 1.21+
- **Database**: PostgreSQL (localhost:5432)

**Data Flow**:
```
GetFollows() → [342 accounts]
  ↓
For each account (concurrent):
  GetAuthorFeed(cursor) → posts
    ↓
  Extract URLs → Scrape metadata (if needed)
    ↓
  Store: posts, links, metadata
    ↓
  Save cursor
```

### Key Metrics

**Performance**:
- Initial run: ~18s (24h ingestion for all accounts)
- Regular run: ~5-10s (only new posts since last cursor)
- Metadata from Bluesky: 96%
- Metadata scraped: 4%

**Error Reduction** (after optimizations):
- Before: 409 errors per 30s test
- After: 14 errors per 30s test
- **96% reduction**

### Technology Stack

**Backend**:
- Go 1.21+
- PostgreSQL 14+
- Libraries:
  - `github.com/spf13/viper` - Configuration
  - `github.com/jmoiron/sqlx` - Database
  - `github.com/PuerkitoBio/goquery` - HTML parsing
  - `github.com/goware/urlx` - URL normalization

**Bluesky AT Protocol**:
- Base URL: `https://bsky.social/xrpc`
- Authentication: JWT tokens
- APIs: `com.atproto.server.createSession`, `app.bsky.feed.getAuthorFeed`, `app.bsky.graph.getFollows`

### Configuration Files

- `config/config.yaml` - Runtime configuration (gitignored)
- `config/config.example.yaml` - Example configuration template
- `migrations/001_initial.sql` - Database schema
- `Makefile` - Build and run commands

### Build Commands

```bash
make build       # Build all binaries (poller, api, migrate)
make run-poller  # Run poller in foreground
make run-api     # Run API server
make migrate     # Apply database migrations
make test        # Run tests
```

## Future ADRs

Planned decision records:
- **ADR 005**: Jetstream Relay Integration (real-time updates)
- **ADR 006**: API Design (REST endpoints for trending links)
- **ADR 007**: Deployment Strategy (Docker, hosting)

## ADR Template

When creating new ADRs, use this template:

```markdown
# ADR XXX: [Title]

**Status**: [Proposed | Accepted | Deprecated | Superseded by ADR-YYY]

**Date**: YYYY-MM-DD

## Context
What is the issue we're trying to solve?

## Decision
What is the change we're proposing?

## Consequences
What becomes easier or harder as a result?

## Alternatives Considered
What other options did we consider?
```

## Contact

For questions about these decisions, see:
- GitHub Issues: https://github.com/petroleumjelliffe/bluesky-news-aggregator/issues
- Code comments in relevant files
- Git commit history for implementation details
