-- Migration 002: Jetstream Firehose Support
-- Creates tables for DID tracking and cursor persistence

-- Table for tracking followed DIDs (replaces poll_state functionality)
CREATE TABLE IF NOT EXISTS follows (
    did TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    display_name TEXT,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TIMESTAMP,
    backfill_completed BOOLEAN DEFAULT FALSE
);

-- Indexes for quick DID and handle lookups
CREATE INDEX idx_follows_did ON follows(did);
CREATE INDEX idx_follows_handle ON follows(handle);

-- Table for Jetstream cursor persistence
CREATE TABLE IF NOT EXISTS jetstream_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    cursor_time_us BIGINT NOT NULL,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT single_row CHECK (id = 1)
);

-- Note: poll_state table is kept for rollback capability
-- It will be truncated (not dropped) when firehose is confirmed working
