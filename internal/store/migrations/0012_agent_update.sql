-- 0012_agent_update (T-092; D-257 … D-259, D-266 … D-268).
--
-- * plan.origin gets 'agent_update': replaces the agent binary of one
--   Target, no document change, no revision. SQLite cannot change a CHECK,
--   so plan is rebuilt (as in 0009), with the images column of 0010.
-- * agent gets what the last hello said, after the guest-text checks of
--   ARCHITECTURE §14. NULL before the first hello of this agent row.
--   capabilities NULL after a hello: the agent sent none (baseline).

CREATE TABLE plan_new (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll', 'rollback', 'import', 'ignore_rule', 'build', 'agent_update')),
    issue_id      INTEGER REFERENCES issue (id),     -- set for revert, accept and ignore_rule
    rollback_to   INTEGER REFERENCES revision (id),  -- the revision n of a rollback (D-131)
    proposal      TEXT NOT NULL,        -- JSON: documents to put and delete
    changes       TEXT NOT NULL,        -- JSON: [{target, resources: [{resource, changes: [wire.Change]}]}]
    risk          TEXT NOT NULL CHECK (risk IN ('read', 'reversible', 'disruptive', 'destructive')),
    -- pending: waits for approval. approved: revision committed (none for
    -- origin build and agent_update), run exists. stale: the head moved
    -- before approval (build: the recipe changed; agent_update: never).
    status        TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'stale')),
    reason        TEXT NOT NULL,
    author        TEXT NOT NULL,        -- principal
    approved_by   TEXT,                 -- principal
    created_at    TEXT NOT NULL,
    approved_at   TEXT,
    human_only    TEXT NOT NULL DEFAULT '[]',  -- JSON array of reasons (D-099, D-201)
    images        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(images) AND json_type(images) = 'array'),
    CHECK ((origin = 'rollback') = (rollback_to IS NOT NULL))
) STRICT;

INSERT INTO plan_new (id, base_revision, origin, issue_id, rollback_to, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at, human_only, images)
SELECT id, base_revision, origin, issue_id, rollback_to, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at, human_only, images FROM plan;

DROP TABLE plan;
ALTER TABLE plan_new RENAME TO plan;

ALTER TABLE agent ADD COLUMN hello_version TEXT;
ALTER TABLE agent ADD COLUMN binary_sha256 TEXT;
ALTER TABLE agent ADD COLUMN capabilities TEXT CHECK (capabilities IS NULL OR json_valid(capabilities));
ALTER TABLE agent ADD COLUMN reverted TEXT CHECK (reverted IS NULL OR json_valid(reverted));
ALTER TABLE agent ADD COLUMN hello_at TEXT;
