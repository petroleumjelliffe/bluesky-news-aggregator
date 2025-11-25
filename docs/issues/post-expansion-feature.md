# Feature: Post Expansion for Link Shares

**Issue**: Users cannot see which specific posts shared a link, or what commentary was included.

**Priority**: High (Roadmap Priority #1 - Frontend Improvements)

---

## Problem Statement

Currently, the aggregator shows:
- Link metadata (title, description, image)
- Share count
- Avatars of sharers

But users cannot:
- See the actual posts that shared the link
- Read commentary from quote posts
- Understand the context around why the link was shared

---

## Requirements

### Functional Requirements

1. **Expandable Link Cards**
   - Each link card should have an "expand" button/indicator
   - Clicking expands to show posts that shared this link
   - Default state: collapsed (to save bandwidth)

2. **Post Display**
   - Show avatar, handle, and post text for each sharer
   - Include quote posts (posts with commentary)
   - Exclude reposts (no commentary, just reshare)
   - Display in reverse chronological order (newest first)

3. **Lazy Loading**
   - Load posts **on-demand** only when user expands
   - Don't fetch posts for collapsed cards
   - Show loading state while fetching

### Technical Requirements

1. **New API Endpoint**
   - `GET /api/links/{id}/posts`
   - Returns list of posts that shared the link
   - Filters out reposts (repost = no text content beyond URL)
   - Includes author metadata (avatar, handle, display name)

2. **Post Types to Include**
   - **Normal posts**: Posts that directly contain the URL
   - **Quote posts**: Posts with URL + commentary text

3. **Post Types to Exclude**
   - **Reposts**: Posts with no original text (just a reshare)

---

## API Specification

### Endpoint: `GET /api/links/{id}/posts`

**Parameters:**
- `id` (path, required): Link ID

**Response:**
```json
{
  "link": {
    "id": 123,
    "url": "https://example.com/article",
    "title": "Article Title"
  },
  "posts": [
    {
      "id": "at://did:plc:xyz.../post/abc123",
      "author": {
        "did": "did:plc:xyz...",
        "handle": "alice.bsky.social",
        "display_name": "Alice",
        "avatar_url": "https://cdn.bsky.app/..."
      },
      "text": "This is interesting! Check this out",
      "created_at": "2024-11-24T10:30:00Z",
      "is_quote_post": true
    }
  ]
}
```

**Filtering Logic:**
- Include posts with `text` that contains the URL
- Include posts with `text` AND commentary (quote posts)
- Exclude posts with ONLY the URL (likely reposts with no commentary)

---

## UI/UX Design

### Link Card States

**Collapsed (Default):**
```
┌─────────────────────────────────────┐
│ 📰 Article Title                    │
│ example.com                          │
│                                      │
│ Brief description of the article... │
│                                      │
│ 👤👤👤 12 shares                      │
│ [▼ Show posts]                      │
└─────────────────────────────────────┘
```

**Expanded:**
```
┌─────────────────────────────────────┐
│ 📰 Article Title                    │
│ example.com                          │
│                                      │
│ Brief description...                 │
│                                      │
│ 👤👤👤 12 shares                      │
│ [▲ Hide posts]                      │
│                                      │
│ ┌─────────────────────────────────┐ │
│ │ 👤 Alice (@alice.bsky.social)   │ │
│ │ This is interesting! Check it... │ │
│ │ 2 hours ago                      │ │
│ └─────────────────────────────────┘ │
│                                      │
│ ┌─────────────────────────────────┐ │
│ │ 👤 Bob (@bob.bsky.social)       │ │
│ │ Great article about...           │ │
│ │ 3 hours ago                      │ │
│ └─────────────────────────────────┘ │
│                                      │
│ [Load more posts]                   │
└─────────────────────────────────────┘
```

### Loading State
```
┌─────────────────────────────────────┐
│ [▲ Hide posts]                      │
│                                      │
│ Loading posts... ⏳                  │
└─────────────────────────────────────┘
```

---

## Implementation Plan

### Phase 1: Backend API
1. Create `GetLinkPosts` database method
   - Query `posts` table joined with `post_links`
   - Filter by link_id
   - Exclude posts with empty/minimal text (reposts)
   - Join with `network_accounts` for author metadata
   - Order by `created_at DESC`

2. Create API endpoint `/api/links/{id}/posts`
   - Parse link ID from URL
   - Call `GetLinkPosts`
   - Return JSON response

3. Add post filtering logic
   - Detect reposts: text is empty or only contains URL
   - Keep quote posts: text contains URL + commentary

### Phase 2: Frontend
1. Update `app.js` to add expand/collapse functionality
   - Add click handler for expand button
   - Toggle card expanded state
   - Fetch posts on first expand (lazy loading)

2. Render post list
   - Display avatar, handle, display name
   - Show post text (truncate if needed)
   - Format timestamps (relative time)

3. Add loading and error states
   - Show spinner while fetching
   - Handle API errors gracefully
   - Cache loaded posts (don't refetch on collapse/expand)

### Phase 3: Optimization
1. Pagination for large share counts
   - Limit initial posts to 10
   - "Load more" button for additional posts

2. Performance
   - Add index on `(link_id, created_at)` for fast queries
   - Consider caching popular link posts

---

## Success Metrics

- Users can see posts for any link with 1 click
- No unnecessary API calls (lazy loading works)
- Quote posts are visible, reposts are excluded
- Load time < 500ms for post expansion

---

## Out of Scope (Future Enhancements)

- Link to original post on Bluesky
- Show post engagement (likes, replies, reposts)
- Filter by post author
- Search within posts
- Real-time updates for new shares

---

## Database Schema Changes

**None required** - All necessary data already exists in:
- `posts` table (has author_did, content, created_at)
- `post_links` table (links posts to links)
- `network_accounts` table (has author metadata)

---

## Testing Plan

1. **Unit Tests**
   - Test post filtering logic (quote vs repost detection)
   - Test database query for link posts

2. **Integration Tests**
   - Test API endpoint returns correct data
   - Test pagination works correctly

3. **Manual Testing**
   - Expand link with many posts
   - Expand link with quote posts
   - Expand link with only reposts (should show message)
   - Test loading states
   - Test error handling

---

## Related Issues

- Roadmap: Frontend Improvements (Priority #1)
- Related: Post deduplication (future)
- Related: Quote post display improvements (future)
