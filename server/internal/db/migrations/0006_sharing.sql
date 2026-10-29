-- Sharing with people and public links (v2, docs/architecture.md §4).
--
-- A share gives one person access to a folder or file and everything in
-- it, on top of what their drive membership gives. Shares follow the item
-- when it moves and end when it is deleted for good.
CREATE TABLE people_shares (
    node_id    TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    access     TEXT NOT NULL CHECK (access IN ('view', 'edit')),
    created_by TEXT REFERENCES users (id),
    created_at TEXT NOT NULL,
    PRIMARY KEY (node_id, user_id)
) STRICT;
CREATE INDEX people_shares_user ON people_shares (user_id);

-- One view-only link per item. The token is kept so the sharer can copy
-- the address again; visits look it up by its SHA-256. expiry is what the
-- sharer chose, expires_at when that runs out, counted from when it was
-- chosen. password_hash is empty when the link has no password.
CREATE TABLE links (
    id            TEXT PRIMARY KEY,
    node_id       TEXT NOT NULL UNIQUE REFERENCES nodes (id) ON DELETE CASCADE,
    token         TEXT NOT NULL,
    token_hash    BLOB NOT NULL UNIQUE,
    expiry        TEXT NOT NULL DEFAULT 'never' CHECK (expiry IN ('never', '7d', '1d')),
    expires_at    TEXT,
    password_hash TEXT NOT NULL DEFAULT '',
    created_by    TEXT REFERENCES users (id),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;
