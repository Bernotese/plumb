-- 0015_restore (T-113; D-290, D-303).
--
-- * restore_state: one row after "plumb restore": the backup it came
--   from and restored mode (1 until POST /restore/confirm).
-- * api_token.disabled_at: a restore disabled the token (401 until a
--   person enables it).
-- * user.locked_at: a restore locked the user (every user but admin).

CREATE TABLE restore_state (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    restored_at       TEXT NOT NULL,
    backup_created_at TEXT NOT NULL,
    header            TEXT NOT NULL CHECK (json_valid(header)),  -- the clear backup header
    restored_mode     INTEGER NOT NULL CHECK (restored_mode IN (0, 1)),
    confirmed_at      TEXT,
    confirmed_by      TEXT,
    confirm_reason    TEXT NOT NULL DEFAULT '',
    CHECK ((restored_mode = 1) = (confirmed_at IS NULL))
) STRICT;

ALTER TABLE api_token ADD COLUMN disabled_at TEXT;
ALTER TABLE user ADD COLUMN locked_at TEXT;
