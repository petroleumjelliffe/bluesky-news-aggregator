# Bluesky News Aggregator Roadmap

**Last Updated**: 2024-11-24

## Completed

- [x] Core aggregator with firehose integration
- [x] Trending links API with share counts
- [x] Link metadata fetching (title, description, OG image)
- [x] Avatar support for sharers
- [x] Domain display under titles
- [x] Template-based frontend
- [x] GitHub Pages deployment (gh-pages branch)
- [x] Render deployment (API + firehose + cron)

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

### 2. Expanded Network Reach

**Goal**: Surface trending content beyond immediate follows.

| Feature | Description | Complexity |
|---------|-------------|------------|
| 2nd-degree network | Friends-of-friends (who your follows follow) | High |
| Global view | Trending across all of Bluesky | Medium |
| Spam/adult filtering | Integrate Bluesky labeler/tagger for content moderation | Medium |

**Technical Notes**:
- 2nd-degree: Crawl follows of follows, store relationship depth
- Global: Remove follows filter, add caching layer (expensive query)
- Filtering: Use `com.atproto.label.queryLabels` or subscribe to labeler firehose
- Consider: Ozone labeler integration for moderation

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

### 4. Syndication

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

1. **Frontend Improvements** - Quick wins, improves existing UX
2. **Multi-User Support** - Enables broader adoption
3. **Expanded Network** - More content discovery options
4. **Syndication** - Passive consumption options

---

## Technical Debt / Infrastructure

- [ ] Add Redis caching for expensive queries
- [ ] Monitoring/alerting (uptime, error rates)
- [ ] Rate limiting per user (not just IP)
- [ ] Database connection pooling tuning
- [ ] Automated backups for Render Postgres

---

## Non-Goals (For Now)

- Mobile app (web-first)
- Real-time WebSocket updates
- User-generated content/comments
- Bookmark/save functionality
- Analytics dashboard
