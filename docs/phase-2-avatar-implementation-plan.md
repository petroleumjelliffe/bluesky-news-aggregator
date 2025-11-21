# Phase 2: Avatar Support Implementation Plan

**Issue**: #24
**Branch**: `feature/ui-phase-2-avatar-support`
**Estimated Time**: 4-5 hours
**Dependencies**: Phase 1 (#23) - Template Refactoring

## Overview

Add avatar support to display circular user avatars next to shared links, showing who in your network shared each link with GitHub-style overlapping avatars.

## Current State Analysis

### Database
- `follows` table **already has** `display_name TEXT` column ✓
- **Missing**: `avatar_url TEXT` column
- Existing indexes: `idx_follows_did`, `idx_follows_handle`

### Types (Already Support Avatars)
- `bluesky.Author` has `Avatar string` field ✓
- `bluesky.Follow` has `Avatar string` field ✓
- `database.TrendingLink` has `Sharers pq.StringArray` (just handles)

### Data Sources
- **Jetstream events**: Provide author info with avatars in real-time
- **Bluesky API**: `GetFollows()` returns `Follow` structs with avatars
- **Backfill**: Uses `GetAuthorFeed()` which returns `Post.Author.Avatar`

## Implementation Plan

### 1. Database Migration

**File**: `migrations/003_add_avatar_support.sql`

```sql
-- Add avatar_url column to follows table
ALTER TABLE follows ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- Create partial index for efficient avatar lookups
CREATE INDEX IF NOT EXISTS idx_follows_avatar
ON follows(did)
WHERE avatar_url IS NOT NULL;

-- Add comment for documentation
COMMENT ON COLUMN follows.avatar_url IS 'User avatar URL from Bluesky profile';
```

**Why not add display_name?**
Already exists! Database check shows:
```
 display_name       | text                        |           |          |
```

### 2. Database Layer Changes

**File**: `internal/database/db.go`

#### Add New Type for Avatar Info
```go
// SharerAvatar represents a user who shared a link with their avatar
type SharerAvatar struct {
    Handle      string  `db:"handle" json:"handle"`
    DisplayName *string `db:"display_name" json:"display_name"`
    AvatarURL   *string `db:"avatar_url" json:"avatar_url"`
    DID         string  `db:"did" json:"did"`
}
```

#### Update TrendingLink struct
```go
type TrendingLink struct {
    ID            int             `db:"id"`
    NormalizedURL string          `db:"normalized_url"`
    OriginalURL   string          `db:"original_url"`
    Title         *string         `db:"title"`
    Description   *string         `db:"description"`
    OGImageURL    *string         `db:"og_image_url"`
    ShareCount    int             `db:"share_count"`
    LastSharedAt  time.Time       `db:"last_shared_at"`
    Sharers       pq.StringArray  `db:"sharers"` // Keep for backward compat
    SharerAvatars []SharerAvatar  // NEW: Full sharer info with avatars
}
```

#### Add New Query Method
```go
// GetLinkSharers retrieves avatar info for users who shared a link
func (db *DB) GetLinkSharers(linkID int, limit int) ([]SharerAvatar, error) {
    query := `
        SELECT DISTINCT
            f.handle,
            f.display_name,
            f.avatar_url,
            f.did
        FROM follows f
        JOIN posts p ON p.author_handle = f.did
        JOIN post_links pl ON pl.post_id = p.id
        WHERE pl.link_id = $1
          AND f.avatar_url IS NOT NULL
        ORDER BY p.created_at DESC
        LIMIT $2
    `

    var sharers []SharerAvatar
    if err := db.conn.Select(&sharers, query, linkID, limit); err != nil {
        return nil, err
    }

    return sharers, nil
}
```

#### Add Avatar Update Method
```go
// UpdateFollowAvatar updates the avatar URL for a follow
func (db *DB) UpdateFollowAvatar(did, avatarURL string) error {
    query := `
        UPDATE follows
        SET avatar_url = $1
        WHERE did = $2
    `

    _, err := db.conn.Exec(query, avatarURL, did)
    return err
}
```

#### Modify GetTrendingLinks
Update the existing query to JOIN and fetch avatars:

```go
func (db *DB) GetTrendingLinks(hoursBack, limit int) ([]TrendingLink, error) {
    query := `
        WITH link_shares AS (
            SELECT
                l.id,
                l.normalized_url,
                l.original_url,
                l.title,
                l.description,
                l.og_image_url,
                COUNT(DISTINCT pl.post_id) as share_count,
                MAX(p.created_at) as last_shared_at,
                ARRAY_AGG(DISTINCT p.author_handle) as sharers
            FROM links l
            JOIN post_links pl ON l.id = pl.link_id
            JOIN posts p ON pl.post_id = p.id
            JOIN follows f ON p.author_handle = f.did
            WHERE p.created_at > NOW() - INTERVAL '$1 hours'
            GROUP BY l.id
            HAVING COUNT(DISTINCT pl.post_id) >= 2
            ORDER BY share_count DESC, last_shared_at DESC
            LIMIT $2
        )
        SELECT * FROM link_shares
    `

    var links []TrendingLink
    if err := db.conn.Select(&links, query, hoursBack, limit); err != nil {
        return nil, err
    }

    // Fetch avatars for each link
    for i := range links {
        avatars, err := db.GetLinkSharers(links[i].ID, 10) // Max 10 avatars
        if err != nil {
            // Log error but don't fail the whole request
            log.Printf("Error fetching avatars for link %d: %v", links[i].ID, err)
            links[i].SharerAvatars = []SharerAvatar{}
        } else {
            links[i].SharerAvatars = avatars
        }
    }

    return links, nil
}
```

### 3. Data Collection Updates

#### A. Update Firehose (cmd/firehose/main.go)

The firehose already processes author data from Jetstream events. We need to extract and store avatars.

**Location to modify**: Where we call `db.AddOrUpdateFollow()`

**Current flow**: Jetstream event → Extract DID/handle → Store in follows table
**New flow**: Jetstream event → Extract DID/handle/**avatar** → Store in follows table

```go
// In processEvent or wherever follows are added
func (f *Firehose) processAuthor(author *processor.Author) error {
    // Add follow if not exists
    if err := f.db.AddOrUpdateFollow(author.DID, author.Handle, author.DisplayName); err != nil {
        return err
    }

    // NEW: Update avatar if present
    if author.Avatar != "" {
        if err := f.db.UpdateFollowAvatar(author.DID, author.Avatar); err != nil {
            log.Printf("Failed to update avatar for %s: %v", author.Handle, err)
        }
    }

    return nil
}
```

**Where does Author come from?**
Check `internal/processor/processor.go` - the firehose already extracts author info from Jetstream events.

#### B. Update Backfill (cmd/backfill/main.go)

The backfill uses `GetFollows()` which already returns avatar data in the `Follow` struct.

**Current**: Fetches follows → Stores DID/handle
**New**: Fetches follows → Stores DID/handle/**avatar**

```go
// In the backfill loop where follows are processed
func processFollows(client *bluesky.Client, db *database.DB, myHandle string) error {
    // ... existing code to get follows list ...

    followsResp, err := client.GetFollows(myHandle)
    // This returns []Follow which has Avatar field!

    for _, follow := range followsResp.Follows {
        // Store basic follow info
        if err := db.AddOrUpdateFollow(follow.DID, follow.Handle, follow.DisplayName); err != nil {
            log.Printf("Error storing follow: %v", err)
            continue
        }

        // NEW: Store avatar if present
        if follow.Avatar != "" {
            if err := db.UpdateFollowAvatar(follow.DID, follow.Avatar); err != nil {
                log.Printf("Error storing avatar for %s: %v", follow.Handle, err)
            }
        }
    }

    return nil
}
```

**Alternatively**, modify `AddOrUpdateFollow()` signature to accept avatar:

```go
// In internal/database/db.go
func (db *DB) AddOrUpdateFollow(did, handle, displayName, avatarURL string) error {
    query := `
        INSERT INTO follows (did, handle, display_name, avatar_url, added_at, last_seen_at)
        VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
        ON CONFLICT (did) DO UPDATE
        SET handle = EXCLUDED.handle,
            display_name = EXCLUDED.display_name,
            avatar_url = EXCLUDED.avatar_url,
            last_seen_at = CURRENT_TIMESTAMP
    `

    _, err := db.conn.Exec(query, did, handle, displayName, avatarURL)
    return err
}
```

**Decision**: Modify `AddOrUpdateFollow()` to include avatar parameter - cleaner and more atomic.

### 4. API Layer Changes

**File**: `cmd/api/main.go`

#### Update Response Type

```go
// SharerAvatar represents a user who shared a link
type SharerAvatar struct {
    Handle      string  `json:"handle"`
    DisplayName *string `json:"display_name"`
    AvatarURL   *string `json:"avatar_url"`
    DID         string  `json:"did"`
}

// LinkResponse is a single link in the API response
type LinkResponse struct {
    ID            int             `json:"id"`
    URL           string          `json:"url"`
    Title         string          `json:"title"`
    Description   string          `json:"description"`
    ImageURL      string          `json:"image_url"`
    ShareCount    int             `json:"share_count"`
    LastSharedAt  string          `json:"last_shared_at"`
    Sharers       []string        `json:"sharers"` // Keep for backward compat
    SharerAvatars []SharerAvatar  `json:"sharer_avatars"` // NEW
}
```

#### Update handleTrending

```go
func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
    // ... existing param parsing ...

    links, err := s.aggregator.GetTrendingLinks(hours, limit)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Convert to API response
    response := TrendingResponse{
        Links: make([]LinkResponse, len(links)),
    }

    for i, link := range links {
        // Convert SharerAvatar from DB to API format
        apiAvatars := make([]SharerAvatar, len(link.SharerAvatars))
        for j, sa := range link.SharerAvatars {
            apiAvatars[j] = SharerAvatar{
                Handle:      sa.Handle,
                DisplayName: sa.DisplayName,
                AvatarURL:   sa.AvatarURL,
                DID:         sa.DID,
            }
        }

        response.Links[i] = LinkResponse{
            ID:            link.ID,
            URL:           link.NormalizedURL,
            Title:         stringOrEmpty(link.Title),
            Description:   stringOrEmpty(link.Description),
            ImageURL:      stringOrEmpty(link.OGImageURL),
            ShareCount:    link.ShareCount,
            LastSharedAt:  link.LastSharedAt.Format(time.RFC3339),
            Sharers:       link.Sharers,
            SharerAvatars: apiAvatars, // NEW
        }
    }

    // ... rest of response ...
}
```

### 5. Frontend Changes

**Files**:
- `cmd/api/static/js/app.js`
- `cmd/api/static/css/styles.css`

#### A. JavaScript - Avatar Stack Rendering

```javascript
// Add to app.js

function renderAvatarStack(sharerAvatars, shareCount) {
    if (!sharerAvatars || sharerAvatars.length === 0) {
        return '';
    }

    const maxVisible = 5;
    const visibleAvatars = sharerAvatars.slice(0, maxVisible);
    const remainingCount = shareCount - maxVisible;

    const avatarHTML = visibleAvatars.map(sharer => {
        const displayName = sharer.display_name || sharer.handle;
        const avatarUrl = sharer.avatar_url || '/static/img/default-avatar.svg';
        const profileUrl = `https://bsky.app/profile/${sharer.handle}`;

        return `
            <a href="${profileUrl}"
               target="_blank"
               rel="noopener noreferrer"
               class="avatar-link"
               title="${displayName} (@${sharer.handle})">
                <img src="${avatarUrl}"
                     alt="${displayName}"
                     class="avatar"
                     onerror="this.src='/static/img/default-avatar.svg'">
            </a>
        `;
    }).join('');

    const moreIndicator = remainingCount > 0
        ? `<span class="avatar-more">+${remainingCount}</span>`
        : '';

    return `
        <div class="avatar-stack">
            ${avatarHTML}
            ${moreIndicator}
        </div>
    `;
}

// Modify loadTrending() to use renderAvatarStack()
function loadTrending() {
    // ... existing fetch code ...

    data.links.forEach(link => {
        const card = document.createElement('div');
        card.className = 'link-card';

        const domain = extractDomain(link.url);
        const avatarStack = renderAvatarStack(link.sharer_avatars, link.share_count);

        card.innerHTML = `
            ${link.image_url ? `
                <div class="link-image">
                    <img src="${link.image_url}" alt="${link.title || 'Link preview'}"
                         onerror="this.parentElement.style.display='none'">
                </div>
            ` : ''}
            <div class="link-content">
                <h3><a href="${link.url}" target="_blank" rel="noopener noreferrer">
                    ${link.title || link.url}
                </a></h3>
                ${domain ? `<div class="link-domain">${domain}</div>` : ''}
                ${link.description ? `<p class="link-description">${link.description}</p>` : ''}

                ${avatarStack}

                <div class="link-meta">
                    <span class="share-count">★ ${link.share_count} share${link.share_count !== 1 ? 's' : ''}</span>
                    <span class="sharers">Shared by: ${link.sharers.slice(0, 3).join(', ')}${link.sharers.length > 3 ? ` and ${link.sharers.length - 3} more` : ''}</span>
                </div>

                <button class="posts-toggle" onclick="togglePosts(this, ${link.id})">
                    Show Posts ▼
                </button>
                <div class="posts-container" id="posts-${link.id}">
                    <div class="loading">Posts will be loaded here in Phase 3...</div>
                </div>
            </div>
        `;

        container.appendChild(card);
    });
}
```

#### B. CSS - Avatar Stack Styling

```css
/* Add to styles.css */

.avatar-stack {
    display: flex;
    align-items: center;
    margin: 12px 0;
    height: 40px;
}

.avatar-link {
    position: relative;
    display: inline-block;
    margin-left: -10px; /* Overlapping effect */
    transition: transform 0.2s ease, z-index 0.2s ease;
    z-index: 1;
}

.avatar-link:first-child {
    margin-left: 0;
}

.avatar-link:hover {
    transform: translateY(-2px);
    z-index: 10;
}

.avatar {
    width: 40px;
    height: 40px;
    border-radius: 50%;
    border: 2px solid white;
    object-fit: cover;
    background: #f0f0f0;
    transition: border-color 0.2s ease;
}

.avatar-link:hover .avatar {
    border-color: #1a73e8;
}

.avatar-more {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 40px;
    height: 40px;
    border-radius: 50%;
    background: #e0e0e0;
    border: 2px solid white;
    font-size: 0.75em;
    font-weight: 600;
    color: #666;
    margin-left: -10px;
}

/* Mobile responsive */
@media (max-width: 768px) {
    .avatar {
        width: 32px;
        height: 32px;
    }

    .avatar-more {
        width: 32px;
        height: 32px;
        font-size: 0.7em;
    }

    .avatar-stack {
        height: 32px;
    }
}
```

#### C. Default Avatar Image

Create a simple SVG default avatar:

**File**: `cmd/api/static/img/default-avatar.svg`

```svg
<svg width="40" height="40" viewBox="0 0 40 40" fill="none" xmlns="http://www.w3.org/2000/svg">
  <circle cx="20" cy="20" r="20" fill="#e0e0e0"/>
  <circle cx="20" cy="16" r="7" fill="#999"/>
  <path d="M8 35C8 28 13 24 20 24C27 24 32 28 32 35" fill="#999"/>
</svg>
```

### 6. Testing Plan

#### Database Tests
```bash
# After migration
psql -d bluesky_news -c "\d follows"
# Should show avatar_url column

psql -d bluesky_news -c "\di idx_follows_avatar"
# Should show partial index
```

#### Manual API Tests
```bash
# Test API response includes avatars
curl "http://localhost:8080/api/trending?hours=24&limit=3" | jq '.links[0].sharer_avatars'

# Should return:
# [
#   {
#     "handle": "user.bsky.social",
#     "display_name": "User Name",
#     "avatar_url": "https://cdn.bsky.app/img/...",
#     "did": "did:plc:..."
#   }
# ]
```

#### Frontend Tests
- [ ] Load page - avatars render
- [ ] Hover avatar - shows tooltip (browser default)
- [ ] Click avatar - opens Bluesky profile in new tab
- [ ] Test with 0 avatars - graceful degradation
- [ ] Test with 1 avatar - single avatar displays
- [ ] Test with 5 avatars - all visible, no "+N"
- [ ] Test with 10+ avatars - shows 5 + "+N more"
- [ ] Test broken image URL - falls back to default avatar
- [ ] Test mobile - avatars scale down properly

#### Data Population
```bash
# Rebuild with avatar support
make build

# Run backfill to populate avatars
make backfill-recent

# Verify avatars in database
psql -d bluesky_news -c "SELECT COUNT(*) FROM follows WHERE avatar_url IS NOT NULL;"

# Should show > 0
```

### 7. Implementation Order

1. **Database Migration** (5 min)
   - Create migration file
   - Run migration
   - Verify schema

2. **Database Layer** (30 min)
   - Add `SharerAvatar` type
   - Add `GetLinkSharers()` method
   - Add `UpdateFollowAvatar()` method
   - Modify `AddOrUpdateFollow()` to include avatar
   - Update `GetTrendingLinks()` to fetch avatars

3. **Data Collection** (45 min)
   - Update firehose to store avatars
   - Update backfill to store avatars
   - Test both data sources

4. **API Layer** (30 min)
   - Add `SharerAvatar` to API types
   - Update `handleTrending()` to include avatars
   - Test API response

5. **Frontend** (90 min)
   - Create default avatar SVG
   - Add CSS for avatar stack
   - Add JavaScript rendering
   - Test with various data scenarios

6. **Testing & Verification** (30 min)
   - Run backfill to populate data
   - Test UI with real data
   - Mobile testing
   - Edge case testing

**Total**: ~4 hours

### 8. Acceptance Criteria

- [ ] Database has `avatar_url` column in `follows` table
- [ ] Migration runs successfully
- [ ] Firehose stores avatars from Jetstream events
- [ ] Backfill stores avatars from Bluesky API
- [ ] API returns `sharer_avatars` array with avatar URLs
- [ ] UI displays avatars as overlapping circles
- [ ] "+N more" indicator shows for 6+ sharers
- [ ] Clicking avatar opens Bluesky profile
- [ ] Broken images fall back to default avatar
- [ ] Mobile responsive (avatars scale down)
- [ ] Hover effect works (translateY)
- [ ] Performance acceptable (avatar queries cached by DB)

### 9. Rollout Strategy

1. **Development** (feature branch)
   - Implement all changes
   - Test locally

2. **Data Population** (on feature branch)
   - Run `make backfill-all` to populate avatars
   - Verify ~80% of follows have avatars

3. **Merge to Main** (after Phase 3?)
   - Could merge Phase 1 + Phase 2 together
   - Or wait for Phase 3 (post details) for full feature set

4. **Production**
   - Run migration
   - Run backfill
   - Deploy API + frontend
   - Monitor error logs for avatar fetch failures

### 10. Future Enhancements

- **Caching**: Cache avatar URLs in Redis to reduce DB queries
- **Lazy Loading**: Don't fetch all avatars upfront, load on scroll
- **Profile Hovers**: Rich profile popups on avatar hover
- **Avatar Updates**: Periodic refresh of avatar URLs (profiles change avatars)
- **Performance**: Denormalize avatar data into a separate table for faster queries

### 11. Risks & Mitigation

**Risk**: Avatar URLs become invalid (404)
**Mitigation**: onerror fallback to default avatar SVG

**Risk**: Performance impact of additional JOIN queries
**Mitigation**: Partial index on avatar_url, limit to 10 avatars per link

**Risk**: Backfill takes long time to populate avatars
**Mitigation**: Run incrementally, handle NULL avatars gracefully in UI

**Risk**: Bluesky avatar URLs change format
**Mitigation**: Store full URL, not parse/construct. Monitor error logs.

## Files to Modify

1. `migrations/003_add_avatar_support.sql` - NEW
2. `internal/database/db.go` - MODIFY (add types, methods, update query)
3. `cmd/firehose/main.go` - MODIFY (store avatars from Jetstream)
4. `cmd/backfill/main.go` - MODIFY (store avatars from API)
5. `cmd/api/main.go` - MODIFY (add types, update response)
6. `cmd/api/static/js/app.js` - MODIFY (render avatars)
7. `cmd/api/static/css/styles.css` - MODIFY (avatar styling)
8. `cmd/api/static/img/default-avatar.svg` - NEW

**Total**: 5 existing files to modify + 2 new files = 7 files

## Dependencies

- Requires Phase 1 (template refactoring) to be merged or on same branch
- No external library dependencies needed
- Uses existing Bluesky API types that already have avatar fields

## Notes

- The Bluesky types already support avatars - this is just plumbing!
- Display name column already exists - only need avatar_url
- Avatar URLs from Bluesky are CDN URLs, highly reliable
- Consider adding avatar URL validation/sanitization for security
