-- Accounts, drives and the file tree (docs/architecture.md §4).
--
-- Times are UTC text in the form 2026-01-02T15:04:05.000Z, so they sort
-- correctly as strings. IDs are UUIDv7 text.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE, -- stored lower case
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    locale        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    disabled_at   TEXT
) STRICT;

-- Only the SHA-256 of the session token is stored.
CREATE TABLE sessions (
    id_hash        BLOB PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at     TEXT NOT NULL,
    last_seen_at   TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    recent_auth_at TEXT NOT NULL,
    user_agent     TEXT NOT NULL,
    ip             TEXT NOT NULL
) STRICT;
CREATE INDEX sessions_user ON sessions (user_id);

-- Second-factor codes sent by email. The id is the challenge the browser
-- holds between the password step and the code step.
CREATE TABLE email_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL,
    code_hash  BLOB NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    used_at    TEXT
) STRICT;
CREATE INDEX email_codes_user ON email_codes (user_id, created_at);

-- A drive and its root folder point at each other; the foreign key from the
-- drive to its root is checked at commit.
CREATE TABLE drives (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('personal', 'shared')),
    name          TEXT NOT NULL,
    owner_user_id TEXT REFERENCES users (id),
    root_id       TEXT NOT NULL REFERENCES nodes (id) DEFERRABLE INITIALLY DEFERRED,
    created_at    TEXT NOT NULL,
    CHECK ((kind = 'personal') = (owner_user_id IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX drives_personal ON drives (owner_user_id) WHERE kind = 'personal';

CREATE TABLE drive_members (
    drive_id TEXT NOT NULL REFERENCES drives (id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role     TEXT NOT NULL CHECK (role IN ('viewer', 'editor', 'manager')),
    PRIMARY KEY (drive_id, user_id)
) STRICT;

-- The folder tree. parent_id is NULL only for a drive's root folder.
-- name_key is the lower-cased name: names are unique per folder ignoring
-- case, among items that aren't in the trash. A trashed folder keeps its
-- children as they are; they are hidden because an ancestor is trashed.
CREATE TABLE nodes (
    id                 TEXT PRIMARY KEY,
    drive_id           TEXT NOT NULL REFERENCES drives (id) DEFERRABLE INITIALLY DEFERRED,
    parent_id          TEXT REFERENCES nodes (id),
    kind               TEXT NOT NULL CHECK (kind IN ('folder', 'file')),
    name               TEXT NOT NULL,
    name_key           TEXT NOT NULL,
    current_version_id TEXT,
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    created_by         TEXT REFERENCES users (id),
    created_at         TEXT NOT NULL,
    modified_at        TEXT NOT NULL,
    trashed_at         TEXT,
    trashed_by         TEXT REFERENCES users (id)
) STRICT;
CREATE UNIQUE INDEX nodes_unique_name ON nodes (parent_id, name_key)
    WHERE trashed_at IS NULL AND parent_id IS NOT NULL;
CREATE INDEX nodes_parent ON nodes (parent_id);
CREATE INDEX nodes_trashed ON nodes (drive_id, trashed_at) WHERE trashed_at IS NOT NULL;

-- Every saved content of a file. Objects are immutable files in the data
-- folder; superseded versions stay until retention removes them.
CREATE TABLE versions (
    id            TEXT PRIMARY KEY,
    node_id       TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    object_id     TEXT NOT NULL,
    size          INTEGER NOT NULL,
    sha256        TEXT NOT NULL,
    created_by    TEXT REFERENCES users (id),
    created_at    TEXT NOT NULL,
    superseded_at TEXT
) STRICT;
CREATE INDEX versions_node ON versions (node_id);

-- Chunked uploads in progress; the bytes live in <data_path>/uploads/<id>.
CREATE TABLE uploads (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_id  TEXT NOT NULL REFERENCES nodes (id),
    name       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    received   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
) STRICT;

CREATE TABLE stars (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, node_id)
) STRICT;

CREATE TABLE recents (
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES nodes (id) ON DELETE CASCADE,
    at      TEXT NOT NULL,
    PRIMARY KEY (user_id, node_id)
) STRICT;

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY,
    at      TEXT NOT NULL,
    user_id TEXT,
    ip      TEXT NOT NULL,
    event   TEXT NOT NULL,
    detail  TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX audit_log_at ON audit_log (at);
