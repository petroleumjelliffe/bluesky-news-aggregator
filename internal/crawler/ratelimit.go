package crawler

import (
	"context"
	"time"
)

// RateLimiter implements a token bucket rate limiter for API calls.
// This ensures we stay within Bluesky's rate limits (~3000 requests per 5 minutes).
type RateLimiter struct {
	tokens   chan struct{}
	interval time.Duration
	done     chan struct{}
}

// NewRateLimiter creates a rate limiter that allows rps requests per second.
// Safe values for Bluesky API: 5-10 requests per second.
func NewRateLimiter(rps int) *RateLimiter {
	rl := &RateLimiter{
		tokens:   make(chan struct{}, rps),
		interval: time.Second / time.Duration(rps),
		done:     make(chan struct{}),
	}

	// Start token refill goroutine
	go rl.refillTokens()

	return rl
}

// refillTokens continuously adds tokens to the bucket at the configured rate
func (rl *RateLimiter) refillTokens() {
	ticker := time.NewTicker(rl.interval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.done:
			return
		case <-ticker.C:
			// Try to add a token, skip if bucket is full
			select {
			case rl.tokens <- struct{}{}:
			default:
			}
		}
	}
}

// Wait blocks until a token is available, respecting context cancellation
func (rl *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-rl.tokens:
		return nil
	}
}

// Close stops the rate limiter
func (rl *RateLimiter) Close() {
	close(rl.done)
}
