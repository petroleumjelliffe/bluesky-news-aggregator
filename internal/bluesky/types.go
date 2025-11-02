package bluesky

import "time"

// Post represents a Bluesky post
type Post struct {
	URI       string     `json:"uri"`
	CID       string     `json:"cid"`
	Author    Author     `json:"author"`
	Record    Record     `json:"record"`
	Embed     *Embed     `json:"embed,omitempty"`
	IndexedAt time.Time  `json:"indexedAt"`
}

// Author represents a post author
type Author struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar,omitempty"`
}

// Record represents the post content
type Record struct {
	Type      string    `json:"$type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

// FeedResponse represents the response from getAuthorFeed
type FeedResponse struct {
	Feed   []FeedItem `json:"feed"`
	Cursor string     `json:"cursor,omitempty"`
}

// FeedItem wraps a post in the feed
type FeedItem struct {
	Post   Post    `json:"post"`
	Reason *Reason `json:"reason,omitempty"`
}

// FollowsResponse represents the response from getFollows
type FollowsResponse struct {
	Subject  Author   `json:"subject"`
	Follows  []Follow `json:"follows"`
	Cursor   string   `json:"cursor,omitempty"`
}

// Follow represents a follow relationship
type Follow struct {
	DID         string    `json:"did"`
	Handle      string    `json:"handle"`
	DisplayName string    `json:"displayName"`
	Avatar      string    `json:"avatar,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// SessionResponse represents authentication response
type SessionResponse struct {
	AccessJWT  string `json:"accessJwt"`
	RefreshJWT string `json:"refreshJwt"`
	Handle     string `json:"handle"`
	DID        string `json:"did"`
}

// Reason represents why a post appears in the feed (e.g., repost)
type Reason struct {
	Type string `json:"$type"`
	By   Author `json:"by,omitempty"`
}

// Embed represents embedded content in a post (quote, external link, images, etc.)
type Embed struct {
	Type   string       `json:"$type"`
	Record *EmbedRecord `json:"record,omitempty"`    // For quote posts
	External *EmbedExternal `json:"external,omitempty"` // For link previews
}

// EmbedRecord represents a quoted post
type EmbedRecord struct {
	Record *Post `json:"record,omitempty"` // The quoted post
}

// EmbedExternal represents an external link with metadata
type EmbedExternal struct {
	URI         string `json:"uri"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Thumb       string `json:"thumb,omitempty"`
}
