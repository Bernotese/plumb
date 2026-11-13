-- 0019_pin_refresh (T-152; D-335, D-346).
--
-- * plan.origin gets 'pin_refresh': POST /images/{name}/refresh or the
--   build schedule changes only the pin of an Image. SQLite cannot change a
--   CHECK, so plan is rebuilt as in 0012 (same columns).
-- * No table for the accepted signature times: they are in the proposal
--   of each approved pin_refresh Plan (Proposal.pins), which no client can
--   set; the engine reads the newest one per Image and source.

CREATE TABLE plan_new (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll', 'rollback', 'import', 'ignore_rule', 'build', 'agent_update', 'pin_refresh')),
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
