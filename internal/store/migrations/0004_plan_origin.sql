-- 0004_plan_origin (D-096): new Plan origins for slice 2. rollback is a
-- Plan from POST /revisions/{n}/rollback (T-031), import from POST /import
-- (T-033), ignore_rule from the ignore action of an Issue (T-032).
--
-- SQLite cannot change a CHECK, so the table is rebuilt. The runner turns
-- foreign keys off for the migration and checks them before the commit
-- (D-115); the ids stay, so revision, run and issue keep their references.

CREATE TABLE plan_new (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll', 'rollback', 'import', 'ignore_rule')),
    issue_id      INTEGER REFERENCES issue (id),     -- set for revert, accept and ignore_rule
    proposal      TEXT NOT NULL,        -- JSON: documents to put and delete
    changes       TEXT NOT NULL,        -- JSON: [{target, resources: [{resource, changes: [wire.Change]}]}]
    risk          TEXT NOT NULL CHECK (risk IN ('read', 'reversible', 'disruptive', 'destructive')),
    -- pending: waits for approval. approved: revision committed, run exists.
    -- stale: the head moved before approval. The result of the apply is run.status.
    -- A proposal that cannot be planned is an API error; it gets no row.
    status        TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'stale')),
    reason        TEXT NOT NULL,
    author        TEXT NOT NULL,        -- principal
    approved_by   TEXT,                 -- principal
    created_at    TEXT NOT NULL,
    approved_at   TEXT
) STRICT;

INSERT INTO plan_new (id, base_revision, origin, issue_id, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at)
SELECT id, base_revision, origin, issue_id, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at FROM plan;

DROP TABLE plan;
ALTER TABLE plan_new RENAME TO plan;
