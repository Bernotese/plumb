-- 0029_guest_backup_pre_restore (T-232; D-420).
--
-- * guest_backup_run.trigger gets 'pre-restore': the safety backup that a
--   restore Run takes of the guest as it is, before it stops, revokes or
--   restores anything. SQLite cannot change a CHECK, so the table is
--   rebuilt (same columns, same rows, same indexes). No new per-Target
--   table: the rows still go with the binding (D-413 rule 2).

CREATE TABLE guest_backup_run_new (
    id          INTEGER PRIMARY KEY,
    target      TEXT NOT NULL,
    lineage     TEXT NOT NULL,
    trigger     TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual', 'pre-restore')),
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    result      TEXT NOT NULL CHECK (result IN ('running', 'done', 'warning', 'failed', 'refused')),
    volid       TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    ctime       INTEGER NOT NULL DEFAULT 0,  -- the volume's ctime (Unix seconds) at finish
    deleted_at  TEXT,                        -- Plumb deleted the volume
    message     TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1024)
) STRICT;

INSERT INTO guest_backup_run_new (id, target, lineage, trigger, started_at, finished_at, result, volid, size, ctime, deleted_at, message)
SELECT id, target, lineage, trigger, started_at, finished_at, result, volid, size, ctime, deleted_at, message FROM guest_backup_run;

DROP TABLE guest_backup_run;
ALTER TABLE guest_backup_run_new RENAME TO guest_backup_run;

CREATE INDEX guest_backup_run_target ON guest_backup_run (target, id);
-- At most one running job per Target.
CREATE UNIQUE INDEX guest_backup_run_one ON guest_backup_run (target) WHERE result = 'running';
