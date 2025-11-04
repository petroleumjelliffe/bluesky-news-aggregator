# UI Improvements Plan

**Status**: Planning
**Date**: 2025-11-04

## Overview

This document outlines planned UI/UX improvements for the Bluesky News Aggregator, including necessary API changes, database modifications, and frontend refactoring.

## Feature Requirements

### 1. Show Link Domain
**User Story**: As a user, I want to see the domain of each link underneath the title in small gray text, so I can quickly identify the source.

**API Changes**: ✅ None needed (domain can be extracted from URL client-side)

**Backend Changes**: None required

**Frontend Changes**:
- Extract domain from URL using `new URL(link.url).hostname`
- Display under title in gray, smaller font
- Example: `example.com` or `nytimes.com`

**Implementation Complexity**: Low

---

### 2. Show Sharer Avatars
**User Story**: As a user, I want to see circular avatars of people who shared each link, so I can quickly recognize who's sharing what.

**Database Changes**:
```sql
-- Add avatar_url column to follows table
ALTER TABLE follows ADD COLUMN avatar_url TEXT;

-- Create index for efficient lookups
CREATE INDEX idx_follows_avatar ON follows(did) WHERE avatar_url IS NOT NULL;
```

**Data Collection**:
- Modify backfill to fetch avatar URLs from Bluesky API (`author.avatar` field)
- Firehose: Extract avatar from Jetstream events (if available) or fetch via API
- Store in `follows.avatar_url`

**API Changes**:
```go
// Add to LinkResponse
type LinkResponse struct {
    // ... existing fields
    SharerAvatars []SharerAvatar `json:"sharer_avatars"`
}

type SharerAvatar struct {
    Handle    string `json:"handle"`
    AvatarURL string `json:"avatar_url"`
    DID       string `json:"did"`  // For deduplication
}
```

**New Database Query**:
```sql
-- Get avatars for sharers of a link
SELECT DISTINCT
    f.handle,
    f.avatar_url,
    f.did
FROM follows f
JOIN posts p ON p.author_handle = f.did
JOIN post_links pl ON pl.post_id = p.id
WHERE pl.link_id = $1
  AND f.avatar_url IS NOT NULL
ORDER BY p.created_at DESC
LIMIT 10;  -- Show max 10 avatars
```

**Frontend Changes**:
- Display avatars as circular images (40x40px)
- Overlap avatars slightly (like GitHub contributors)
- Show "+N more" indicator if > 5 avatars
- Tooltip on hover showing handle

**Implementation Complexity**: Medium

---

### 3. Expandable Posts View
**User Story**: As a user, I want to click on a link card to expand and see the actual posts that contained the link, so I can read the context and commentary.

**Database Changes**: None (data already exists in `posts` and `post_links`)

**API Changes**:
```go
// New endpoint: GET /api/links/{link_id}/posts
type LinkPostsResponse struct {
    Link  LinkDetail  `json:"link"`
    Posts []PostDetail `json:"posts"`
}

type LinkDetail struct {
    ID          int    `json:"id"`
    URL         string `json:"url"`
    Title       string `json:"title"`
    Description string `json:"description"`
    ImageURL    string `json:"image_url"`
}

type PostDetail struct {
    ID            string    `json:"id"`
    AuthorHandle  string    `json:"author_handle"`
    AuthorDID     string    `json:"author_did"`
    AvatarURL     string    `json:"avatar_url"`
    DisplayName   string    `json:"display_name"`
    Content       string    `json:"content"`
    CreatedAt     string    `json:"created_at"`
    PostURL       string    `json:"post_url"`  // bsky.app URL
    IsQuotePost   bool      `json:"is_quote_post"`  // Has additional commentary
    IsRepost      bool      `json:"is_repost"`      // Simple share
}
```

**New Database Query**:
```sql
-- Get all posts for a link with author details
SELECT
    p.id,
    p.author_handle,
    p.content,
    p.created_at,
    f.handle,
    f.display_name,
    f.avatar_url,
    f.did,
    -- Determine if it's a quote post (has text beyond URL)
    CASE
        WHEN LENGTH(TRIM(REGEXP_REPLACE(p.content, 'https?://[^\s]+', '', 'g'))) > 10
        THEN true
        ELSE false
    END as is_quote_post
FROM posts p
JOIN post_links pl ON p.id = pl.post_id
LEFT JOIN follows f ON p.author_handle = f.did
WHERE pl.link_id = $1
ORDER BY p.created_at DESC
LIMIT 50;
```

**Frontend Changes**:
- Add "Show Posts" button/toggle on each link card
- Expand card to show nested post list
- Each post shows:
  - Avatar, display name, handle
  - Post content (full text)
  - Timestamp
  - Link to view on bsky.app
  - Badge if it's a quote post (has commentary)
- Collapse button to hide posts

**Implementation Complexity**: Medium-High

---

### 4. Differentiate Reposts vs Quote Posts
**User Story**: As a user, I want to easily see which shares have additional commentary (quote posts) vs simple reposts.

**Backend Logic**:
- Analyze post content to detect if it's just a URL vs URL + text
- Heuristic: If content minus URLs has >10 chars, it's a quote post
- Alternative: Check if post is linked as quote in database (requires tracking quote relationships)

**API Changes**: Already covered in #3 (`is_quote_post` field)

**Frontend Changes**:
- Show "💬 Quote" badge for quote posts
- Show "🔄 Share" badge for simple reposts
- Style quote posts differently (e.g., border color)
- Count breakdown: "5 quotes, 3 shares"

**Implementation Complexity**: Low (piggybacks on #3)

---

### 5. Global Trending View
**User Story**: As a user, I want to see what's trending across ALL of Bluesky, not just my network.

**Database Changes**:
```sql
-- New table for global trending (optional optimization)
CREATE TABLE global_trending_cache (
    link_id INTEGER REFERENCES links(id),
    share_count INTEGER NOT NULL,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (link_id)
);

CREATE INDEX idx_global_trending_count ON global_trending_cache(share_count DESC);
```

**API Changes**:
```go
// Modify existing endpoint to support scope parameter
// GET /api/trending?scope=network|global&hours=24&limit=20

type TrendingResponse struct {
    Scope string         `json:"scope"`  // "network" or "global"
    Links []LinkResponse `json:"links"`
}
```

**New Database Query**:
```sql
-- Global trending (all posts, not just followed users)
SELECT
    l.id,
    l.normalized_url,
    l.title,
    l.description,
    l.og_image_url,
    COUNT(DISTINCT pl.post_id) as share_count,
    MAX(p.created_at) as last_shared_at,
    ARRAY_AGG(DISTINCT p.author_handle) as sharers
FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
WHERE p.created_at > NOW() - INTERVAL '$1 hours'
GROUP BY l.id
HAVING COUNT(DISTINCT pl.post_id) >= 2  -- Minimum threshold
ORDER BY share_count DESC, last_shared_at DESC
LIMIT $2;
```

**Frontend Changes**:
- Add tab/toggle: "My Network" vs "All of Bluesky"
- Different styling for global view (maybe different color scheme)
- Note: "Based on followed accounts' networks" for global view

**Implementation Complexity**: Medium

**Performance Considerations**:
- Global query is expensive (scans all posts)
- Solution 1: Add caching layer (Redis)
- Solution 2: Materialize global_trending_cache table (refresh every 5 min)
- Solution 3: Limit global view to last 24 hours only

---

### 6. Template-Based Frontend
**User Story**: As a developer, I want the HTML template separated from Go code for easier editing.

**File Structure**:
```
cmd/api/
  main.go           # Router and handlers only
  templates/
    index.html      # Main template
    link_card.html  # Partial for link card
    post_detail.html # Partial for post detail
  static/
    css/
      styles.css
    js/
      app.js
```

**Backend Changes**:
```go
import "html/template"

// Load templates
var templates = template.Must(template.ParseGlob("cmd/api/templates/*.html"))

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
    data := struct {
        Title string
    }{
        Title: "Bluesky News Aggregator",
    }

    if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}
```

**Implementation Complexity**: Low

**Benefits**:
- Easier to edit HTML/CSS
- Can use Go template partials for reusability
- Separates concerns (routing vs presentation)

---

## Implementation Phases

### Phase 1: Quick Wins (Est. 2-3 hours)
1. ✅ Show domain under title (frontend only)
2. ✅ Template refactoring (move HTML to files)
3. ✅ Basic post expansion UI (no API yet)

### Phase 2: Avatar Support (Est. 4-5 hours)
1. Database migration (add avatar_url column)
2. Update backfill to fetch avatars
3. Update firehose to store avatars
4. Modify API to return sharer avatars
5. Frontend avatar display with overlap

### Phase 3: Post Details (Est. 5-6 hours)
1. Create `/api/links/{id}/posts` endpoint
2. Implement post details query
3. Add quote post detection logic
4. Build expandable UI with post list
5. Add badges for quote vs repost

### Phase 4: Global Trending (Est. 6-8 hours)
1. Implement global trending query
2. Add caching layer (optional)
3. Create scope toggle in UI
4. Performance testing and optimization

---

## Database Schema Changes Summary

```sql
-- Add avatar support
ALTER TABLE follows ADD COLUMN avatar_url TEXT;
CREATE INDEX idx_follows_avatar ON follows(did) WHERE avatar_url IS NOT NULL;

-- Optional: Global trending cache
CREATE TABLE global_trending_cache (
    link_id INTEGER REFERENCES links(id),
    share_count INTEGER NOT NULL,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (link_id)
);
CREATE INDEX idx_global_trending_count ON global_trending_cache(share_count DESC);
```

---

## API Endpoints Summary

### Existing (Modified)
- `GET /api/trending?hours=24&limit=20&scope=network|global`
  - Add `scope` parameter
  - Add `sharer_avatars` to response
  - Add `domain` field (or extract client-side)

### New
- `GET /api/links/{link_id}/posts`
  - Returns all posts that shared a link
  - Includes author details, avatars, timestamps
  - Marks quote posts vs simple shares

---

## Frontend Architecture

### Current (Inline HTML)
```
cmd/api/main.go
  └─ 400 lines of HTML/CSS/JS in handleRoot()
```

### Proposed (Template-Based)
```
cmd/api/
  main.go (handlers only)
  templates/
    base.html          # Layout wrapper
    index.html         # Main page
    components/
      link-card.html   # Reusable link card
      post-item.html   # Reusable post item
      avatar-stack.html # Avatar overlap component
  static/
    css/
      main.css         # Core styles
      components.css   # Component-specific styles
    js/
      api.js           # API client
      ui.js            # UI interactions
      components/
        linkCard.js    # Link card component
        postList.js    # Post list component
```

---

## Visual Design Notes

### Domain Display
```
[Link Title]
example.com                     ← Gray, 0.85em font size
```

### Avatar Stack
```
🟦🟥🟩 +7 more
← Overlapping circles, 40px each, -10px margin
```

### Expandable Posts
```
[Link Card]
  [Show 12 Posts ▼]              ← Clickable toggle

[Link Card - Expanded]
  [Hide Posts ▲]

  ┌─────────────────────────────┐
  │ 👤 Alice (@alice.bsky.so... │ 💬 Quote
  │ "This is fascinating! The...│
  │ 2 hours ago · View on Bluesky│
  ├─────────────────────────────┤
  │ 👤 Bob (@bob.com)            │ 🔄 Share
  │ [Link only, no commentary]   │
  │ 5 hours ago · View on Bluesky│
  └─────────────────────────────┘
```

### Global vs Network Toggle
```
[ My Network ]  [ All of Bluesky ]
    ↑ Active         ↑ Inactive
```

---

## Testing Plan

### Unit Tests
- [ ] Domain extraction from various URL formats
- [ ] Quote post detection heuristic
- [ ] Avatar URL validation

### Integration Tests
- [ ] `/api/links/{id}/posts` endpoint
- [ ] Global trending query performance
- [ ] Template rendering

### Manual Testing
- [ ] Avatar display with 0, 1, 5, 10+ sharers
- [ ] Post expansion/collapse interaction
- [ ] Network vs global toggle
- [ ] Mobile responsiveness

---

## Performance Considerations

### Avatar Storage
- Store avatar URLs, not download images
- Cache avatar lookups (most users appear multiple times)
- Lazy load avatars in UI

### Post Details
- Limit to 50 posts per link initially
- Paginate if >50 posts
- Consider WebSocket for real-time updates

### Global Trending
- **Critical**: Expensive query across all posts
- Solution: Materialized view refreshed every 5 minutes
- Alternative: Limit to last 6 hours only
- Consider separate worker process for global aggregation

---

## Future Enhancements

### Post Phase 4
- Real-time updates via WebSocket
- Sentiment analysis on quote posts
- Filter by post type (quote only / share only)
- Export trending links as RSS/JSON
- User preferences (hide domains, filter keywords)
- Dark mode
- Share buttons (Twitter, Mastodon, etc.)

---

## Questions for Review

1. **Avatar data**: Should we store `display_name` in follows table too, or always join?
2. **Post content**: Should we store cleaned post text (URLs removed) for quote detection?
3. **Global scope**: Should it be limited to "followed users' networks" or truly global?
4. **Performance**: Should we implement caching in Phase 4 or wait for performance issues?
5. **UI Framework**: Stay vanilla JS or introduce lightweight framework (Alpine.js, Petite Vue)?

---

## Dependencies

### Backend
- Existing: `chi` router, `viper` config
- New: `html/template` (stdlib)

### Frontend
- Existing: Vanilla JavaScript, inline CSS
- New (optional): CSS framework like Tailwind or keep custom?

### Database
- PostgreSQL 12+ (for array aggregation)

---

## Rollout Strategy

1. **Phase 1** (Quick wins) → Deploy to production immediately
2. **Phase 2** (Avatars) → Deploy when avatar backfill complete
3. **Phase 3** (Post details) → Beta test with limited users
4. **Phase 4** (Global) → Performance test on staging first

---

## Success Metrics

- **User Engagement**: Click-through rate on "Show Posts"
- **Performance**: API response time <200ms for trending
- **Quality**: >80% of links have avatars visible
- **Adoption**: Global view usage vs network view ratio
