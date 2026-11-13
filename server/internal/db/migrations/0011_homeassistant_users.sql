-- Home Assistant users (v2.8, docs/architecture.md §5).
--
-- An admin can add someone with their Home Assistant username instead of
-- inviting them. The account has no Homedrive password (password_hash is
-- empty, as for an invitation that isn't accepted yet) and logs in with
-- Home Assistant, still followed by the email code. ha_only = 1 tells it
-- apart from an open invitation. Such an account stays a member and stays
-- linked; choosing a password through an emailed link makes it a normal
-- account (0). The check keeps that true for every way the row can change.
ALTER TABLE users ADD COLUMN ha_only INTEGER NOT NULL DEFAULT 0
    CHECK (ha_only = 0 OR (ha_only = 1 AND password_hash = '' AND role = 'member' AND ha_username IS NOT NULL));
