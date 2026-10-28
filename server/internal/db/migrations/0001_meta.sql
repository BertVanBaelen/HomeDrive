-- Installation-wide key/value settings, such as instance_id.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
