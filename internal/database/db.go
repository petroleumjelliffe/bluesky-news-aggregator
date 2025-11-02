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
			ARRAY_AGG(DISTINCT p.author_handle) as sharers
		FROM links l
		JOIN post_links pl ON l.id = pl.link_id
		JOIN posts p ON pl.post_id = p.id
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
