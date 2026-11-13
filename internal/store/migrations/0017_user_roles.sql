-- 0017_user_roles (T-147; D-329, D-330, D-342, D-343).
--
-- * user.role: read < plan < apply < admin, on the scale of the token
--   tiers. Every user that exists before this migration is admin: up to
--   now every session had the tier admin.
-- * user.disabled_at: a person disabled the user (no login; the name stays
--   taken, so a new user never inherits tokens, rules or history).
-- * user.created_by: the principal that made the user ('system' for the
--   user of the installer and for every user from before this migration).
-- * user.last_login_at: the last good login.

ALTER TABLE user ADD COLUMN role TEXT NOT NULL DEFAULT 'admin'
    CHECK (role IN ('read', 'plan', 'apply', 'admin'));
ALTER TABLE user ADD COLUMN disabled_at TEXT;
ALTER TABLE user ADD COLUMN created_by TEXT NOT NULL DEFAULT 'system';
ALTER TABLE user ADD COLUMN last_login_at TEXT;
