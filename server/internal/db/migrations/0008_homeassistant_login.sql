-- Home Assistant login (v2, docs/architecture.md §5).
--
-- The Home Assistant username a user may log in with, stored lower case.
-- NULL: not linked. SQLite can't add a UNIQUE column, so an index does it.
ALTER TABLE users ADD COLUMN ha_username TEXT;
CREATE UNIQUE INDEX users_ha_username ON users (ha_username) WHERE ha_username IS NOT NULL;
