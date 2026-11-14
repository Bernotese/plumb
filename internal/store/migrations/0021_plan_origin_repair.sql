-- 0021_plan_origin_repair (T-190; D-372, D-373, D-385).
--
-- * plan.origin gets 'repair' (POST /targets/{name}/repair, T-190) and
--   'adopt' (POST /targets/{name}/adopt, T-193), in one rebuild. SQLite
--   cannot change a CHECK, so plan is rebuilt as in 0019 (same columns).
--   A repair Plan commits no revision, like build and agent_update.

CREATE TABLE plan_new (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll', 'rollback', 'import', 'ignore_rule', 'build', 'agent_update', 'pin_refresh', 'repair', 'adopt')),
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
