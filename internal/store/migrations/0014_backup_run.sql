-- 0014_backup_run (T-112; D-288, D-302).
--
-- * backup_run: one row per backup that serve started (schedule or
--   POST /backups). GET /backups shows the newest as last_run. A row that
--   is still running at the start of serve was cut by a stop and is set
--   to failed. A backup holds its own row as running (the snapshot is
--   taken inside the run).
-- * trigger: schedule (the daily backup of serve), api (POST /backups) or
--   cli (plumb backup now). Only schedule counts for the schedule.

CREATE TABLE backup_run (
    id          INTEGER PRIMARY KEY,
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    result      TEXT NOT NULL CHECK (result IN ('running', 'ok', 'failed')),
    by          TEXT NOT NULL,          -- principal: system, user:<name>, token:<id>
    trigger     TEXT NOT NULL CHECK (trigger IN ('schedule', 'api', 'cli')),
    name        TEXT NOT NULL,          -- the file name (backup.NameRE)
    message     TEXT NOT NULL DEFAULT '',
    CHECK ((result = 'running') = (finished_at IS NULL))
) STRICT;
