-- 0020_setting (T-174; D-361, D-374).
--
-- Controller settings that a person changes in Plumb (PATCH /settings).
-- One row per key; value is JSON. A row with value 'null' means "not set"
-- (back to the flag, env or built-in default); the row stays, so the time
-- and the author of the last change are kept. The table is in the backup
-- (the whole database), not in the export.

CREATE TABLE setting (
    key        TEXT PRIMARY KEY CHECK (key IN ('backup.time', 'backup.keep')),
    value      TEXT NOT NULL CHECK (json_valid(value)),
    changed_by TEXT NOT NULL,   -- principal
    changed_at TEXT NOT NULL
) STRICT;
