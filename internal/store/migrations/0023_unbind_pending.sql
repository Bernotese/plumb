-- 0023_unbind_pending (T-205; D-396, D-388, ARCHITECTURE §19).
--
-- * unbind_pending: the agent of a deleted adopted Target whose unbind was
--   not confirmed. Its credential stays valid, but the link lets it do one
--   thing only: take unbind and send its result. The row goes when the
--   result is confirmed, or when an admin forgets the guest's agent.
-- * run.warnings: the steps of a Run that ended `warning` (an applied step
--   that left something for a person, D-396), so no list shows a clean
--   success.
-- * run_step.status gets 'warning'. SQLite cannot change a CHECK, so
--   run_step is rebuilt as plan in 0021 (same columns; nothing refers to
--   run_step).

CREATE TABLE unbind_pending (
    target    TEXT PRIMARY KEY,
    agent_id  TEXT NOT NULL,
    guest_ref TEXT NOT NULL,
    node      TEXT NOT NULL,
    since     TEXT NOT NULL
);

ALTER TABLE run ADD COLUMN warnings INTEGER NOT NULL DEFAULT 0;

CREATE TABLE run_step_new (
    run_id      INTEGER NOT NULL REFERENCES run (id),
    seq         INTEGER NOT NULL,       -- order of creation inside the run
    target      TEXT NOT NULL,
    resource    TEXT NOT NULL,          -- resource id
    -- wire.Status values. refused: a change was above the approved risk.
    -- warning: done, but left something for a person (D-396).
    status      TEXT NOT NULL CHECK (status IN ('pending', 'running', 'unchanged', 'applied', 'failed', 'refused', 'skipped', 'warning')),
    changes     TEXT NOT NULL DEFAULT '[]',   -- JSON: [wire.Change]; the action is wire.ActionOf(changes)
    output      TEXT NOT NULL DEFAULT '',
    started_at  TEXT,
    finished_at TEXT,
    rolled_back INTEGER NOT NULL DEFAULT 0 CHECK (rolled_back IN (0, 1)),
    PRIMARY KEY (run_id, seq)
) STRICT, WITHOUT ROWID;

INSERT INTO run_step_new (run_id, seq, target, resource, status, changes, output, started_at, finished_at, rolled_back)
SELECT run_id, seq, target, resource, status, changes, output, started_at, finished_at, rolled_back FROM run_step;

DROP TABLE run_step;
ALTER TABLE run_step_new RENAME TO run_step;
