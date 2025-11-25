# ADR 008: OAuth and Multi-User Support

**Status**: Proposed
**Date**: 2024-11-24
**Author**: Claude + petroleumjelliffe

## Context

The current system uses a single Bluesky account (via environment variables) to fetch follows and filter the firehose. To support multiple users seeing their own network's trending links, we need:

1. OAuth authentication with Bluesky
2. Per-user follow lists
3. Secure token storage
4. Efficient data model that avoids duplication

## Decision

### Authentication Flow

Use Bluesky's OAuth 2.0 PKCE flow for browser-based authentication.

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Browser   │     │   Our API   │     │  Bluesky    │
└──────┬──────┘     └──────┬──────┘     └──────┬──────┘
       │                   │                   │
       │  1. Click Login   │                   │
       │──────────────────>│                   │
       │                   │                   │
       │  2. Redirect to   │                   │
       │     Bluesky OAuth │                   │
       │<──────────────────│                   │
       │                   │                   │
       │  3. User authorizes                   │
       │──────────────────────────────────────>│
       │                   │                   │
       │  4. Callback with code                │
       │<──────────────────────────────────────│
       │                   │                   │
       │  5. POST code     │                   │
       │──────────────────>│                   │
       │                   │  6. Exchange code │
       │                   │──────────────────>│
       │                   │                   │
       │                   │  7. Access token  │
       │                   │<──────────────────│
       │                   │                   │
       │  8. Set session   │                   │
       │     cookie        │                   │
       │<──────────────────│                   │
       │                   │                   │
```

### Database Schema

#### Shared Follows Pool (No Duplication)

```sql
-- Users table
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    did TEXT UNIQUE NOT NULL,
    handle TEXT NOT NULL,
    display_name TEXT,
    avatar_url TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_login_at TIMESTAMP,
    follows_synced_at TIMESTAMP  -- When we last fetched their follows
);

-- OAuth tokens (encrypted at rest)
CREATE TABLE user_tokens (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    access_token_encrypted BYTEA NOT NULL,
    refresh_token_encrypted BYTEA NOT NULL,
    token_expires_at TIMESTAMP NOT NULL,
    refresh_expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- User follows junction table (lightweight, just IDs)
CREATE TABLE user_follows (
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    follow_did TEXT NOT NULL,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, follow_did)
);

CREATE INDEX idx_user_follows_did ON user_follows(follow_did);

-- Global account metadata pool (shared across all users)
-- Renamed from 'follows' to 'accounts' for clarity
CREATE TABLE accounts (
    did TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    display_name TEXT,
    avatar_url TEXT,
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

#### Key Design Decisions

1. **Shared Account Pool**: The `accounts` table stores metadata (handle, avatar, display_name) once per DID, shared across all users who follow that account.

2. **Lightweight Junction**: `user_follows` only stores `(user_id, follow_did)` - no duplication of account metadata.

3. **Encrypted Tokens**: OAuth tokens stored encrypted using AES-256-GCM with a server-side key from environment variable.

### Migration Path

```sql
-- Rename existing follows table
ALTER TABLE follows RENAME TO accounts;

-- Remove user-specific columns from accounts
ALTER TABLE accounts DROP COLUMN IF EXISTS backfill_completed;

-- Create new tables
CREATE TABLE users (...);
CREATE TABLE user_tokens (...);
CREATE TABLE user_follows (...);

-- Migrate existing single-user data
INSERT INTO users (did, handle, created_at)
VALUES ('admin', 'admin', CURRENT_TIMESTAMP);

INSERT INTO user_follows (user_id, follow_did)
SELECT 1, did FROM accounts;
```

### Security Considerations

#### Token Security

| Concern | Mitigation |
|---------|------------|
| Token theft | Encrypt at rest with AES-256-GCM |
| Token in logs | Never log tokens, use token IDs |
| Token in URL | Use POST for token exchange, httpOnly cookies |
| XSS attacks | httpOnly + Secure + SameSite=Strict cookies |
| CSRF attacks | Require Origin header validation |
| Token expiry | Refresh tokens before expiry, handle refresh failures |

```go
// Token encryption
type TokenStore struct {
    key []byte // 32 bytes from TOKEN_ENCRYPTION_KEY env var
}

func (ts *TokenStore) Encrypt(token string) ([]byte, error) {
    block, _ := aes.NewCipher(ts.key)
    gcm, _ := cipher.NewGCM(block)
    nonce := make([]byte, gcm.NonceSize())
    io.ReadFull(rand.Reader, nonce)
    return gcm.Seal(nonce, nonce, []byte(token), nil), nil
}
```

#### Session Security

```go
// Session cookie settings
http.SetCookie(w, &http.Cookie{
    Name:     "session",
    Value:    sessionToken,
    HttpOnly: true,           // No JS access
    Secure:   true,           // HTTPS only
    SameSite: http.SameSiteStrictMode,
    MaxAge:   86400 * 30,     // 30 days
    Path:     "/",
})
```

#### API Security

```go
// Auth middleware
func (s *Server) authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Public endpoints don't need auth
        if isPublicEndpoint(r.URL.Path) {
            next.ServeHTTP(w, r)
            return
        }

        session, err := s.validateSession(r)
        if err != nil {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }

        ctx := context.WithValue(r.Context(), "user", session.User)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### Follows Sync Strategy

Since follows don't change often, we minimize API calls:

```go
type FollowsSyncConfig struct {
    // Sync on login if older than this
    SyncOnLoginIfOlderThan time.Duration // 24 hours

    // Background sync interval
    BackgroundSyncInterval time.Duration // 7 days

    // Manual refresh cooldown
    ManualRefreshCooldown time.Duration // 1 hour
}
```

#### Sync Flow

1. **On Login**: If `follows_synced_at` > 24 hours ago, sync follows
2. **Background Job**: Weekly sync for active users
3. **Manual Refresh**: Rate-limited to once per hour per user
4. **Stale Cleanup**: Remove accounts not followed by any user (monthly)

```sql
-- Find orphaned accounts (not followed by anyone)
DELETE FROM accounts
WHERE did NOT IN (SELECT DISTINCT follow_did FROM user_follows)
  AND did NOT IN (SELECT did FROM users);
```

### API Changes

#### New Endpoints

```
GET  /auth/login          # Redirect to Bluesky OAuth
GET  /auth/callback       # OAuth callback handler
POST /auth/logout         # Clear session
GET  /auth/me             # Current user info

POST /api/follows/sync    # Manual refresh (rate-limited)
GET  /api/follows         # List user's follows
```

#### Modified Endpoints

```
GET /api/trending?hours=24&limit=20
    # If authenticated: filter to user's follows
    # If not authenticated: return error or global trending (future)
```

### Frontend Pages

#### Logged Out State

```
┌────────────────────────────────────────────┐
│  Bluesky News Aggregator                   │
│                                            │
│  Discover what's trending in your network  │
│                                            │
│  ┌────────────────────────────────┐        │
│  │  🔵 Login with Bluesky         │        │
│  └────────────────────────────────┘        │
│                                            │
│  • See links shared by people you follow   │
│  • Discover trending content               │
│  • No signup required                      │
│                                            │
└────────────────────────────────────────────┘
```

#### Logged In State

```
┌────────────────────────────────────────────┐
│  Bluesky News Aggregator    👤 @user ▼     │
│                                            │
│  [Time Range ▼] [Limit ▼] [Refresh]        │
│                                            │
│  Following 342 accounts                    │
│  Last synced: 2 hours ago  [↻ Sync]        │
│                                            │
│  ┌─ Link Card ─────────────────────────┐   │
│  │ ...                                 │   │
│  └─────────────────────────────────────┘   │
└────────────────────────────────────────────┘
```

### Query Optimization

#### Trending Links for User

```sql
-- Get trending links for a specific user
WITH user_followed_dids AS (
    SELECT follow_did FROM user_follows WHERE user_id = $1
)
SELECT
    l.id,
    l.normalized_url,
    l.title,
    l.description,
    l.og_image_url,
    COUNT(DISTINCT p.id) as share_count,
    MAX(p.created_at) as last_shared_at
FROM links l
JOIN post_links pl ON l.id = pl.link_id
JOIN posts p ON pl.post_id = p.id
WHERE p.author_handle IN (SELECT follow_did FROM user_followed_dids)
  AND p.created_at > NOW() - INTERVAL '$2 hours'
GROUP BY l.id
HAVING COUNT(DISTINCT p.id) >= 2
ORDER BY share_count DESC, last_shared_at DESC
LIMIT $3;
```

#### Index for Performance

```sql
-- Composite index for user's trending query
CREATE INDEX idx_posts_author_created
ON posts(author_handle, created_at DESC);

-- For finding sharers
CREATE INDEX idx_user_follows_user_id
ON user_follows(user_id);
```

### Firehose Changes

The firehose now processes ALL posts (not just from one user's follows):

```go
// Option 1: Process all posts, filter at query time
// Pros: Simple, one firehose for all users
// Cons: More data stored

// Option 2: Only process posts from DIDs followed by at least one user
func (f *Firehose) shouldProcess(did string) bool {
    // Cache this query result
    return f.didManager.IsFollowedByAnyUser(did)
}
```

We'll use Option 2 with caching:

```go
type DIDManager struct {
    followedDIDs map[string]bool  // All DIDs followed by any user
    mu           sync.RWMutex
    refreshedAt  time.Time
}

func (dm *DIDManager) RefreshFromDB(db *database.DB) error {
    dids, err := db.GetAllFollowedDIDs()
    // ...
}
```

### Environment Variables

```bash
# New required variables
TOKEN_ENCRYPTION_KEY=<32-byte-hex-key>  # For encrypting OAuth tokens
SESSION_SECRET=<random-string>           # For signing session cookies
OAUTH_CLIENT_ID=<bluesky-client-id>      # From Bluesky OAuth registration
OAUTH_REDIRECT_URI=https://example.com/auth/callback
```

## Consequences

### Positive

- Multiple users can see their own network's trending links
- No data duplication for users following same accounts
- Follows sync is efficient (infrequent, cached)
- Secure token storage with encryption at rest
- Clean separation between user data and shared data

### Negative

- More complex queries (need to join through user_follows)
- Need to manage token refresh lifecycle
- Firehose needs to track "followed by any user" set
- More infrastructure (encryption keys, session management)

### Risks

| Risk | Mitigation |
|------|------------|
| Token encryption key lost | Document key backup procedure |
| OAuth flow breaks | Graceful fallback to logged-out state |
| Follows sync overloads Bluesky API | Rate limit syncs, queue with backoff |
| Large user base = large follows set | Add pagination, consider sharding |

## Alternatives Considered

### 1. Duplicate Follows Per User

Each user gets their own copy of follows table entries.

- **Rejected because**: Wasteful for users following same accounts, harder to update shared data (avatars, handles)

### 2. No Server-Side Auth (Client-Only OAuth)

Store tokens only in browser, API is stateless.

- **Rejected because**: Can't run background jobs, can't do server-side filtering for performance, security concerns with client-side tokens

### 3. Use Bluesky App Passwords

Instead of OAuth, have users create app passwords.

- **Rejected because**: Less secure (full access), poor UX, no standard refresh mechanism

## Implementation Order

1. **Database migration**: Add users, user_tokens, user_follows tables
2. **Token encryption**: Implement secure token storage
3. **OAuth flow**: Login/callback/logout endpoints
4. **Session middleware**: Auth validation for protected endpoints
5. **Follows sync**: Initial sync on login, background refresh
6. **API updates**: Filter trending by user's follows
7. **Frontend**: Login button, logged-in state, sync controls
8. **Firehose update**: Process posts from any followed DID

## Open Questions

1. Should logged-out users see anything? (Global trending? Demo data?)
2. Rate limits per user vs per IP?
3. How to handle users with 10,000+ follows? (Pagination, limits?)
4. Should we support "remember me" with longer sessions?
