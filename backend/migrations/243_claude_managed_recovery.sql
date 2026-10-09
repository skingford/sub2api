-- Client IDs are namespaced by authenticated ownership. Upstream UUIDs remain
-- globally unique and keep their immutable binding in claude_session_ownership.
CREATE TABLE claude_recovery_conversations (
 id UUID PRIMARY KEY,
 user_id BIGINT NOT NULL CHECK (user_id > 0),
 group_id BIGINT NOT NULL CHECK (group_id > 0),
 client_session_id UUID NOT NULL,
 route TEXT NOT NULL,
 model TEXT NOT NULL,
 active_session_id UUID NOT NULL UNIQUE,
 generation BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'ready',
 lease_token UUID,
 lease_until TIMESTAMPTZ,
 last_operation UUID,
 material BYTEA,
 source BYTEA,
 source_version BIGINT NOT NULL DEFAULT 0,
 checkpoint BYTEA,
 checkpoint_version BIGINT NOT NULL DEFAULT 0,
 summary_state TEXT NOT NULL DEFAULT 'idle',
 summary_lease UUID,
 summary_lease_until TIMESTAMPTZ,
 migration_blocked BOOLEAN NOT NULL DEFAULT FALSE,
 expires_at TIMESTAMPTZ NOT NULL,
 UNIQUE (user_id, group_id, client_session_id),
 CHECK (state IN ('ready','busy','uncertain','expired'))
);
CREATE TABLE claude_recovery_segments (
 session_id UUID PRIMARY KEY,
 conversation_id UUID NOT NULL REFERENCES claude_recovery_conversations(id),
 generation BIGINT NOT NULL,
 account_id BIGINT,
 principal_hash TEXT NOT NULL DEFAULT '',
 restore BYTEA,
 predecessor UUID,
 reason TEXT NOT NULL DEFAULT 'initial',
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE (conversation_id, generation)
);
CREATE TABLE claude_recovery_operations (
 id UUID PRIMARY KEY,
 conversation_id UUID NOT NULL REFERENCES claude_recovery_conversations(id),
 session_id UUID NOT NULL,
 idempotency_key TEXT,
 idempotency_hash TEXT,
 request_digest TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'prepared',
 result BYTEA,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 expires_at TIMESTAMPTZ NOT NULL,
 UNIQUE (conversation_id, idempotency_key),
 CHECK (state IN ('prepared','sent','completed','failed','uncertain'))
);
CREATE TABLE claude_recovery_summary_budget (
 user_id BIGINT NOT NULL CHECK (user_id > 0),
 day DATE NOT NULL,
 calls INTEGER NOT NULL,
 input_tokens BIGINT NOT NULL DEFAULT 0,
 output_tokens BIGINT NOT NULL DEFAULT 0,
 unknown_outcomes INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY (user_id, day)
);
CREATE INDEX claude_recovery_pending_summary ON claude_recovery_conversations(summary_state,summary_lease_until);
CREATE INDEX claude_recovery_expiry ON claude_recovery_conversations(expires_at) WHERE state <> 'expired';
CREATE INDEX claude_recovery_operation_expiry ON claude_recovery_operations(expires_at);
