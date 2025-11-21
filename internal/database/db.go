package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// DB wraps the database connection
type DB struct {
	*sqlx.DB
}

// Post represents a Bluesky post in the database
type Post struct {
	ID           string    `db:"id"`
	AuthorHandle string    `db:"author_handle"`
	Content      string    `db:"content"`
	CreatedAt    time.Time `db:"created_at"`
	IndexedAt    time.Time `db:"indexed_at"`
}

// Link represents a URL shared in posts
type Link struct {
	ID            int       `db:"id"`
	OriginalURL   string    `db:"original_url"`
	NormalizedURL string    `db:"normalized_url"`
	Title         *string   `db:"title"`
	Description   *string   `db:"description"`
	OGImageURL    *string   `db:"og_image_url"`
	FirstSeenAt   time.Time `db:"first_seen_at"`
	LastFetchedAt *time.Time `db:"last_fetched_at"`
}

// PostLink represents the relationship between posts and links
type PostLink struct {
	PostID string `db:"post_id"`
	LinkID int    `db:"link_id"`
}

// TrendingLink represents an aggregated link with share count
type TrendingLink struct {
	ID            int            `db:"id"`
	NormalizedURL string         `db:"normalized_url"`
	OriginalURL   string         `db:"original_url"`
	Title         *string        `db:"title"`
	Description   *string        `db:"description"`
	OGImageURL    *string        `db:"og_image_url"`
	ShareCount    int            `db:"share_count"`
	LastSharedAt  time.Time      `db:"last_shared_at"`
	Sharers       pq.StringArray `db:"sharers"`
}

// Follow represents a followed account (DID)
type Follow struct {
	DID               string     `db:"did"`
	Handle            string     `db:"handle"`
	DisplayName       *string    `db:"display_name"`
	AvatarURL         *string    `db:"avatar_url"`
	AddedAt           time.Time  `db:"added_at"`
	LastSeenAt        *time.Time `db:"last_seen_at"`
	BackfillCompleted bool       `db:"backfill_completed"`
}

// SharerAvatar represents a user who shared a link with their avatar
type SharerAvatar struct {
	Handle      string  `db:"handle" json:"handle"`
	DisplayName *string `db:"display_name" json:"display_name"`
	AvatarURL   *string `db:"avatar_url" json:"avatar_url"`
	DID         string  `db:"did" json:"did"`
}

// NewDB creates a new database connection
func NewDB(connectionString string) (*DB, error) {
	db, err := sqlx.Connect("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test the connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{db}, nil
}

// InsertPost inserts a new post into the database
func (db *DB) InsertPost(post *Post) error {
	query := `
		INSERT INTO posts (id, author_handle, content, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
	`

	_, err := db.Exec(query, post.ID, post.AuthorHandle, post.Content, post.CreatedAt)
	return err
}

// GetOrCreateLink gets an existing link or creates a new one
func (db *DB) GetOrCreateLink(originalURL, normalizedURL string) (*Link, error) {
	link := &Link{}

	// Try to get existing
	query := `SELECT * FROM links WHERE normalized_url = $1`
	err := db.Get(link, query, normalizedURL)

	if err == sql.ErrNoRows {
		// Create new
		query = `
			INSERT INTO links (original_url, normalized_url)
			VALUES ($1, $2)
			RETURNING *
		`
		err = db.Get(link, query, originalURL, normalizedURL)
	}

	return link, err
}

// UpdateLinkMetadata updates the OpenGraph metadata for a link
func (db *DB) UpdateLinkMetadata(linkID int, title, description, imageURL string) error {
	query := `
		UPDATE links
		SET title = $1, description = $2, og_image_url = $3, last_fetched_at = NOW()
		WHERE id = $4
	`

	_, err := db.Exec(query, title, description, imageURL, linkID)
	return err
}

// MarkLinkFetched marks a link as having been fetched (even if fetch failed)
func (db *DB) MarkLinkFetched(linkID int) error {
	query := `UPDATE links SET last_fetched_at = NOW() WHERE id = $1`
	_, err := db.Exec(query, linkID)
	return err
}

// LinkPostToLink creates a relationship between a post and a link
func (db *DB) LinkPostToLink(postID string, linkID int) error {
	query := `
		INSERT INTO post_links (post_id, link_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`

	_, err := db.Exec(query, postID, linkID)
	return err
}

// GetTrendingLinks retrieves the most-shared links within a time window
func (db *DB) GetTrendingLinks(hoursBack int, limit int) ([]TrendingLink, error) {
	query := `
		SELECT
			l.id,
			l.normalized_url,
			l.original_url,
			l.title,
			l.description,
			l.og_image_url,
			COUNT(DISTINCT pl.post_id) as share_count,
			MAX(p.created_at) as last_shared_at,
			ARRAY_AGG(DISTINCT COALESCE(f.handle, p.author_handle)) as sharers
		FROM links l
		JOIN post_links pl ON l.id = pl.link_id
		JOIN posts p ON pl.post_id = p.id
		LEFT JOIN follows f ON p.author_handle = f.did
		WHERE p.created_at > NOW() - INTERVAL '1 hour' * $1
		GROUP BY l.id
		ORDER BY share_count DESC, last_shared_at DESC
		LIMIT $2
	`

	var links []TrendingLink
	err := db.Select(&links, query, hoursBack, limit)
	return links, err
}

// GetLastCursor retrieves the last cursor for a user handle
func (db *DB) GetLastCursor(handle string) (string, error) {
	var cursor sql.NullString
	query := `SELECT last_cursor FROM poll_state WHERE user_handle = $1`
	err := db.Get(&cursor, query, handle)

	if err == sql.ErrNoRows {
		return "", nil
	}

	if !cursor.Valid {
		return "", err
	}

	return cursor.String, err
}

// UpdateCursor updates the cursor for a user handle
func (db *DB) UpdateCursor(handle, cursor string) error {
	query := `
		INSERT INTO poll_state (user_handle, last_cursor, last_polled_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_handle)
		DO UPDATE SET last_cursor = $2, last_polled_at = NOW()
	`

	_, err := db.Exec(query, handle, cursor)
	return err
}

// GetAllFollows returns all followed DIDs
func (db *DB) GetAllFollows() ([]Follow, error) {
	var follows []Follow
	query := `SELECT * FROM follows ORDER BY handle`
	err := db.Select(&follows, query)
	return follows, err
}

// AddFollow adds a new follow to the database
func (db *DB) AddFollow(did, handle string, displayName *string, avatarURL *string) error {
	query := `
		INSERT INTO follows (did, handle, display_name, avatar_url, added_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (did)
		DO UPDATE SET handle = $2, display_name = $3, avatar_url = $4
	`
	_, err := db.Exec(query, did, handle, displayName, avatarURL)
	return err
}

// RemoveFollow removes a follow from the database
func (db *DB) RemoveFollow(did string) error {
	query := `DELETE FROM follows WHERE did = $1`
	_, err := db.Exec(query, did)
	return err
}

// UpdateFollowLastSeen updates the last_seen_at timestamp for a DID
func (db *DB) UpdateFollowLastSeen(did string) error {
	query := `UPDATE follows SET last_seen_at = NOW() WHERE did = $1`
	_, err := db.Exec(query, did)
	return err
}

// MarkBackfillCompleted marks a follow as having completed backfill
func (db *DB) MarkBackfillCompleted(did string) error {
	query := `UPDATE follows SET backfill_completed = TRUE WHERE did = $1`
	_, err := db.Exec(query, did)
	return err
}

// GetJetstreamCursor retrieves the last cursor for Jetstream
func (db *DB) GetJetstreamCursor() (*int64, error) {
	var cursor sql.NullInt64
	query := `SELECT cursor_time_us FROM jetstream_state WHERE id = 1`
	err := db.Get(&cursor, query)

	if err == sql.ErrNoRows {
		return nil, nil // No cursor yet
	}

	if err != nil {
		return nil, err
	}

	if !cursor.Valid {
		return nil, nil
	}

	val := cursor.Int64
	return &val, nil
}

// UpdateJetstreamCursor updates the cursor for Jetstream
func (db *DB) UpdateJetstreamCursor(cursorTimeUS int64) error {
	query := `
		INSERT INTO jetstream_state (id, cursor_time_us, last_updated)
		VALUES (1, $1, NOW())
		ON CONFLICT (id)
		DO UPDATE SET cursor_time_us = $1, last_updated = NOW()
	`
	_, err := db.Exec(query, cursorTimeUS)
	return err
}

// GetLinkSharers retrieves users who shared a specific link with their avatar info
func (db *DB) GetLinkSharers(linkID int) ([]SharerAvatar, error) {
	query := `
		SELECT DISTINCT
			COALESCE(f.handle, p.author_handle) as handle,
			f.display_name,
			f.avatar_url,
			COALESCE(f.did, p.author_handle) as did
		FROM post_links pl
		JOIN posts p ON pl.post_id = p.id
		LEFT JOIN follows f ON p.author_handle = f.did
		WHERE pl.link_id = $1
		ORDER BY handle
	`

	var sharers []SharerAvatar
	err := db.Select(&sharers, query, linkID)
	return sharers, err
}
