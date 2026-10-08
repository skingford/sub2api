-- Durable ownership is deliberately independent of expiring scheduler caches.
-- Keep tombstones when accounts or API keys are deleted: deleting an account
-- must never make its old conversations eligible for another account.
CREATE TABLE IF NOT EXISTS claude_session_ownership (
    session_id UUID PRIMARY KEY,
    account_id BIGINT NOT NULL CHECK (account_id > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
