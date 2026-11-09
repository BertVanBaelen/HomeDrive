-- Editing (v2, docs/architecture.md "Editors, versions and edit locks").
--
-- One person edits a file at a time. The lock's ID is a secret only its
-- holder knows, so only its SHA-256 is kept. A lock that ran out stays
-- until someone else takes the file, so a holder who comes back (a laptop
-- that slept) can carry on.
CREATE TABLE edit_locks (
    node_id     TEXT PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    lock_hash   TEXT NOT NULL,
    expires_at  TEXT NOT NULL
) STRICT;

-- The lock (hash) an editing session saved a version under: the next save
-- of the same session replaces that version instead of adding another.
ALTER TABLE versions ADD COLUMN edit_lock TEXT;

-- The file an upload replaces ("Replace" when the name is taken); the upload
-- becomes a new file if that file is deleted meanwhile.
ALTER TABLE uploads ADD COLUMN replace_node_id TEXT REFERENCES nodes (id) ON DELETE SET NULL;
