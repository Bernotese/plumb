-- 0020_setting (T-174; D-361, D-374).
--
-- Controller settings that a person changes in Plumb (PATCH /settings).
-- One row per key; value is JSON. A row with value 'null' means "not set"
-- (back to the flag, env or built-in default); the row stays, so the time
-- and the author of the last change are kept. The table is in the backup
-- (the whole database), not in the export.
-- backup.due_by is internal (never in the API): after a change of the
-- time, the next slot of the old time, so a change never makes the next
-- backup later than the old schedule would (review n1 of #282).

CREATE TABLE setting (
    key        TEXT PRIMARY KEY CHECK (key IN ('backup.time', 'backup.keep', 'backup.due_by')),
    value      TEXT NOT NULL CHECK (json_valid(value)),
    changed_by TEXT NOT NULL,   -- principal
    changed_at TEXT NOT NULL
) STRICT;
