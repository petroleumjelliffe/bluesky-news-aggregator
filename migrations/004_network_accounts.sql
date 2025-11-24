-- Migration 004: Network Accounts for 2nd-Degree Support
-- This table tracks both 1st-degree (direct follows) and 2nd-degree (friends-of-friends)
-- accounts for discovering trending content from extended networks.

CREATE TABLE IF NOT EXISTS network_accounts (
    did TEXT PRIMARY KEY,
    handle TEXT NOT NULL,
    display_name TEXT,
    avatar_url TEXT,

    -- Network metadata
    degree INTEGER NOT NULL DEFAULT 1,  -- 1 = direct follow, 2 = friend-of-friend
    source_count INTEGER NOT NULL DEFAULT 1,  -- How many 1st-degree accounts follow this

    -- Track which 1st-degree accounts link to this (for display/debugging)
    -- stored as JSON array of DIDs
    source_dids JSONB DEFAULT '[]',

    -- Timestamps
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for efficient filtering
CREATE INDEX IF NOT EXISTS idx_network_degree ON network_accounts(degree);
CREATE INDEX IF NOT EXISTS idx_network_source_count ON network_accounts(source_count DESC);
CREATE INDEX IF NOT EXISTS idx_network_degree_count ON network_accounts(degree, source_count DESC);

-- Comments for documentation
COMMENT ON TABLE network_accounts IS 'Tracks both 1st and 2nd-degree network accounts with source counts';
COMMENT ON COLUMN network_accounts.degree IS '1 = direct follow, 2 = friend-of-friend';
COMMENT ON COLUMN network_accounts.source_count IS 'Number of 1st-degree accounts that follow this account (for 2nd-degree filtering)';
COMMENT ON COLUMN network_accounts.source_dids IS 'JSON array of DIDs of 1st-degree accounts that follow this account';

-- Function to update last_updated_at timestamp
CREATE OR REPLACE FUNCTION update_network_accounts_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.last_updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger to auto-update timestamp
CREATE TRIGGER network_accounts_updated_at_trigger
    BEFORE UPDATE ON network_accounts
    FOR EACH ROW
    EXECUTE FUNCTION update_network_accounts_updated_at();
