-- Tokens for devices that can't hold a session, such as the Apple Shortcut
-- that uploads from an iPhone's share sheet. Scope "upload" only allows
-- adding files to the owner's Inbox. Only the SHA-256 is stored.
CREATE TABLE device_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   BLOB NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('upload')),
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
) STRICT;
CREATE INDEX device_tokens_user ON device_tokens (user_id);
