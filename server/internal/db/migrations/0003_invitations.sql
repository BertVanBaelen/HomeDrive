-- One-time links to set a password: invitations for new accounts and
-- password resets made by an admin. Only the SHA-256 of the token is
-- stored. A user who hasn't accepted their invitation has an empty
-- password_hash and can't sign in.
CREATE TABLE invitations (
    token_hash BLOB PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('invite', 'reset')),
    created_by TEXT REFERENCES users (id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;
CREATE INDEX invitations_user ON invitations (user_id);
