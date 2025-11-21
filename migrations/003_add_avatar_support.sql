-- Migration 003: Add avatar support to follows table
-- This migration adds avatar_url column to store user avatars from Bluesky profiles

-- Add avatar_url column to follows table
ALTER TABLE follows ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- Create partial index for efficient avatar lookups
-- Only indexes rows that have avatars (saves space and improves performance)
CREATE INDEX IF NOT EXISTS idx_follows_avatar
ON follows(did)
WHERE avatar_url IS NOT NULL;

-- Add comment for documentation
COMMENT ON COLUMN follows.avatar_url IS 'User avatar URL from Bluesky profile (populated by backfill)';
