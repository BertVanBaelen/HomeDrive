-- Thumbnails and previews (v2, docs/architecture.md §4).
--
-- The length of a video or audio version, read by ffprobe when its
-- thumbnail is made. The thumbnails themselves live in the cache folder,
-- not in the database.
CREATE TABLE media (
    version_id  TEXT PRIMARY KEY REFERENCES versions (id) ON DELETE CASCADE,
    duration_ms INTEGER NOT NULL
) STRICT;
