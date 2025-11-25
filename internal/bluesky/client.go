package bluesky

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a Bluesky API client
type Client struct {
	httpClient *http.Client
	baseURL    string
	handle     string
	did        string
	jwt        string
}

// NewClient creates a new Bluesky client and authenticates
func NewClient(handle, password string) (*Client, error) {
	client := &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://bsky.social/xrpc",
		handle:     handle,
	}

	if err := client.authenticate(password); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}

// authenticate logs in and stores the JWT token
func (c *Client) authenticate(password string) error {
	url := fmt.Sprintf("%s/com.atproto.server.createSession", c.baseURL)

	payload := map[string]string{
		"identifier": c.handle,
		"password":   password,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed with status: %d", resp.StatusCode)
	}

	var session SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return err
	}

	c.jwt = session.AccessJWT
	c.did = session.DID
	return nil
}

// GetDID returns the authenticated user's DID
func (c *Client) GetDID() string {
	return c.did
}

// GetAuthorFeed fetches posts from a specific author
func (c *Client) GetAuthorFeed(handle string, cursor string, limit int) (*FeedResponse, error) {
	url := fmt.Sprintf("%s/app.bsky.feed.getAuthorFeed?actor=%s&limit=%d",
		c.baseURL, handle, limit)

	if cursor != "" {
		url += fmt.Sprintf("&cursor=%s", cursor)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.jwt)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var feedResp FeedResponse
	if err := json.NewDecoder(resp.Body).Decode(&feedResp); err != nil {
		return nil, err
	}

	return &feedResp, nil
}

// GetFollows fetches the list of accounts that a user follows (handles only)
func (c *Client) GetFollows(handle string) ([]string, error) {
	follows, err := c.GetFollowsWithMetadata(handle)
	if err != nil {
		return nil, err
	}

	handles := make([]string, len(follows))
	for i, follow := range follows {
		handles[i] = follow.Handle
	}

	return handles, nil
}

// GetFollowsWithMetadata fetches the full follow objects with metadata (DID, avatar, etc.)
func (c *Client) GetFollowsWithMetadata(handle string) ([]Follow, error) {
	var allFollows []Follow
	cursor := ""

	for {
		url := fmt.Sprintf("%s/app.bsky.graph.getFollows?actor=%s&limit=100",
			c.baseURL, handle)

		if cursor != "" {
			url += fmt.Sprintf("&cursor=%s", cursor)
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Set("Authorization", "Bearer "+c.jwt)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			// Read error response body for debugging
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("API error: %d, body: %s", resp.StatusCode, string(bodyBytes))
		}

		var followsResp FollowsResponse
		if err := json.NewDecoder(resp.Body).Decode(&followsResp); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		allFollows = append(allFollows, followsResp.Follows...)

		if followsResp.Cursor == "" {
			break
		}
		cursor = followsResp.Cursor

		// Rate limiting
		time.Sleep(100 * time.Millisecond)
	}

	return allFollows, nil
}
