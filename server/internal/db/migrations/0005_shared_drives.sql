-- Shared drives with members and roles (v2, docs/architecture.md §4).
--
-- The invite form ticks the drives marked join_by_default; new users no
-- longer join a drive automatically.
ALTER TABLE drives ADD COLUMN join_by_default INTEGER NOT NULL DEFAULT 0 CHECK (join_by_default IN (0, 1));

-- v1 had one shared drive, "Family", with every user as editor. It stays
-- the drive new users get by default, and the admins become its managers.
UPDATE drives SET join_by_default = 1
WHERE id = (SELECT id FROM drives WHERE kind = 'shared' ORDER BY created_at LIMIT 1);

UPDATE drive_members SET role = 'manager'
WHERE user_id IN (SELECT id FROM users WHERE role = 'admin')
  AND drive_id IN (SELECT id FROM drives WHERE kind = 'shared');

-- v1 stored the name in English and translated it on screen. From now on
-- the stored name is shown, so it follows the language of the admin who set
-- Homedrive up, like a new installation.
UPDATE drives SET name = 'Familie'
WHERE kind = 'shared' AND name = 'Family'
  AND (SELECT locale FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1) = 'nl';

CREATE INDEX drive_members_user ON drive_members (user_id);
