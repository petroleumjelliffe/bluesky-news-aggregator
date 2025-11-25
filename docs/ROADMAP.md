# Bluesky News Aggregator Roadmap

**Last Updated**: 2024-11-24

## Completed

### Core Features
- [x] Core aggregator with firehose integration
- [x] Trending links API with share counts
- [x] Link metadata fetching (title, description, OG image)
- [x] Avatar support for sharers
- [x] Domain display under titles
- [x] Template-based frontend
- [x] GitHub Pages deployment (gh-pages branch)
- [x] Render deployment (API + firehose + cron)

### Network Discovery (v2.0.0)
- [x] 2nd-degree network crawling (friends-of-friends)
- [x] Degree-based filtering (1st, 2nd, or global views)
- [x] Source count filtering (e.g., followed by 2+ friends)
- [x] Profile metadata fetching (handles, avatars, display names)
- [x] Network management CLI tools
- [x] API endpoints with `?degree=` parameter
- [x] 49,831 accounts monitored (343 1st-degree + 49,488 2nd-degree)

---

## Upcoming Features

### 1. Multi-User Support

**Goal**: Allow multiple users to see trending links from their own networks.

| Feature | Description | Complexity |
|---------|-------------|------------|
| OAuth login | Replace env var credentials with Bluesky OAuth flow | High |
| User accounts | Store user sessions, associate follows with users | Medium |
| Per-user follows | Each user sees their own network's trending links | Medium |
| Lists/Starterpacks | Support Bluesky lists or starterpacks without auth | Medium |

**Technical Notes**:
- Bluesky OAuth: Use `atproto` OAuth 2.0 PKCE flow
- Database: Add `users` table, add `user_id` FK to `follows`
- API: Add auth middleware, scope trending queries by user
- Lists: Fetch list members via `app.bsky.graph.getList`

---

### 2. Content Moderation & Filtering

**Goal**: Improve content quality and safety.

| Feature | Description | Complexity |
|---------|-------------|------------|
| Spam/bot filtering | Detect and filter spam accounts and bot content | Medium |
| Adult content filtering | Integrate Bluesky labeler for NSFW content | Medium |
| Domain blocklist | Allow filtering/hiding certain domains | Low |
| Label-based filtering | Use Bluesky's labeling system for moderation | Medium |

**Technical Notes**:
- Spam detection: Track posting frequency, repetitive content patterns
- Labeler integration: Subscribe to labeler firehose or query `com.atproto.label.queryLabels`
- Ozone integration: Consider Ozone moderation service for automated filtering
- Domain blocklist: Simple table with blocked domains, check during link processing

---

### 3. Frontend Improvements

**Goal**: Better UX for exploring shared content.

| Feature | Description | Complexity |
|---------|-------------|------------|
| Load posts | Expand link card to show actual posts that shared it | Medium |
| Dedupe reposts | Distinguish reposts vs quote posts, group them | Low |
| Quote post display | Show commentary from quote posts | Low |

**Technical Notes**:
- New endpoint: `GET /api/links/{id}/posts`
- Quote detection: Check if post text has content beyond URL
- UI: Expandable cards with post list, badges for quote vs repost

---

### 4. Performance & Scalability

**Goal**: Optimize for growing network size and user base.

| Feature | Description | Complexity |
|---------|-------------|------------|
| Redis caching | Cache trending queries, reduce DB load | Medium |
| Materialized views | Pre-compute trending rankings | Low |
| Query optimization | Add indexes, optimize slow queries | Low |
| Connection pooling | Tune database connection pool settings | Low |

**Technical Notes**:
- Redis: Cache trending results with TTL, invalidate on new posts
- Materialized views: Refresh every N minutes for fast trending queries
- Indexes: Add composite indexes on (created_at, author_degree) for filtering
- Monitoring: Track query performance, identify bottlenecks

---

### 5. Syndication

**Goal**: Deliver trending links outside the web app.

| Feature | Description | Complexity |
|---------|-------------|------------|
| Daily digest email | Send top N links from last 24h | Medium |
| Slack integration | Post digest to Slack channel | Low |
| RSS feed | `/feed.xml` with trending links | Low |

**Technical Notes**:
- Email: Use SendGrid/Resend, cron job at configured time
- Slack: Incoming webhook, format as Slack blocks
- RSS: Generate Atom/RSS XML from trending endpoint

---

## Priority Order

1. **Frontend Improvements** - Quick wins, improves existing UX for 2nd-degree content
2. **Content Moderation** - Essential for quality as network grows to 49k+ accounts
3. **Performance & Scalability** - Optimize queries for large dataset (160k+ posts)
4. **Multi-User Support** - Enables broader adoption
5. **Syndication** - Passive consumption options

---

## Technical Debt / Infrastructure

**High Priority:**
- [ ] Add indexes for degree-filtered queries (created_at + author_degree)
- [ ] Monitoring/alerting (uptime, error rates, firehose lag)
- [ ] Query performance profiling with 160k+ posts

**Medium Priority:**
- [ ] Redis caching for trending queries
- [ ] Rate limiting per user (not just IP)
- [ ] Database connection pooling tuning
- [ ] Automated backups for Render Postgres

**Nice to Have:**
- [ ] Prometheus metrics export
- [ ] Grafana dashboards
- [ ] Log aggregation (structured logging)

---

## Non-Goals (For Now)

- Mobile app (web-first)
- Real-time WebSocket updates
- User-generated content/comments
- Bookmark/save functionality
- Analytics dashboard
