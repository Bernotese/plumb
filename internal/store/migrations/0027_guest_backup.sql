-- 0027_guest_backup (T-221; D-405 … D-409, D-417, ARCHITECTURE §20).
--
-- * guest_backup_lineage: the backup lineage of a Target's guest. It
--   follows the binding generation (D-397, D-417): a new generation (a new
--   guest of the Target, also on the same VMID) gets a new lineage, so the
--   old guest's volumes are "older guest" and never touched. The row goes
--   with the binding (DeleteBinding: Target delete, unbind of an adopted
--   Target), so a new Target of the name, or on a reused VMID, starts a
--   new lineage too.
-- * guest_backup_run: one backup job of a Target (scheduled or manual).
--   Goes with the binding. The trigger is also in the volume's marker
--   (":m"), so a lost row (a controller restore) does not change retention.

CREATE TABLE guest_backup_lineage (
    target     TEXT PRIMARY KEY,
    gen        INTEGER NOT NULL,
    lineage    TEXT NOT NULL CHECK (length(lineage) = 16),
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE guest_backup_run (
    id          INTEGER PRIMARY KEY,
    target      TEXT NOT NULL,
    lineage     TEXT NOT NULL,
    trigger     TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual')),
    started_at  TEXT NOT NULL,
    finished_at TEXT,
    result      TEXT NOT NULL CHECK (result IN ('running', 'done', 'warning', 'failed', 'refused')),
    volid       TEXT NOT NULL DEFAULT '',
    message     TEXT NOT NULL DEFAULT '' CHECK (length(message) <= 1024)
) STRICT;

CREATE INDEX guest_backup_run_target ON guest_backup_run (target, id);
-- At most one running job per Target.
CREATE UNIQUE INDEX guest_backup_run_one ON guest_backup_run (target) WHERE result = 'running';
