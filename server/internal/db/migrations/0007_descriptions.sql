-- Descriptions and saved links (v2, docs/architecture.md §4).
--
-- A description belongs to the item, not to a version. description_key is
-- the lower-cased copy that search matches, like name_key. An upload keeps
-- the description it was started with until it becomes a file.
ALTER TABLE nodes ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN description_key TEXT NOT NULL DEFAULT '';
ALTER TABLE uploads ADD COLUMN description TEXT NOT NULL DEFAULT '';

-- What a saved link (a .url file) points at, for showing it as a card
-- without reading the file. Written whenever a .url version is saved; site
-- and author come from the oEmbed provider when there is one, fetched_at
-- is when that last answered.
CREATE TABLE link_previews (
    node_id    TEXT PRIMARY KEY REFERENCES nodes (id) ON DELETE CASCADE,
    url        TEXT NOT NULL,
    site       TEXT NOT NULL,
    author     TEXT NOT NULL DEFAULT '',
    fetched_at TEXT
) STRICT;
