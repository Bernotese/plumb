-- 0009_image_version (T-059; D-198, D-199, D-210): image builds.
-- (Numbered 0008 until T-070's 0008_agent_addrs is on main; pl-lead
-- renumbers before the merge.)
--
-- * image_version: one row per build of an Image. The number counts up per
--   Image name and is never used again: rows are never deleted, also not
--   when the Image document is deleted (status 'removed'). The row holds no
--   VMID (D-188): the driver finds the template by name and digest.
-- * image_schedule: the last schedule slot that was planned per Image.
-- * plan.origin gets 'build': a manual or scheduled build with no document
--   change. SQLite cannot change a CHECK, so plan is rebuilt (as in 0004).

CREATE TABLE image_version (
    image         TEXT NOT NULL,            -- Image document name
    version       INTEGER NOT NULL CHECK (version >= 1),
    status        TEXT NOT NULL CHECK (status IN ('building', 'ready', 'failed', 'removed')),
    trigger       TEXT NOT NULL CHECK (trigger IN ('change', 'manual', 'schedule')),
    run_id        INTEGER NOT NULL REFERENCES run (id),
    revision_id   INTEGER NOT NULL REFERENCES revision (id),  -- the pinned head of the Run
    recipe_sha256 TEXT NOT NULL CHECK (length(recipe_sha256) = 64),
    base_sha256   TEXT NOT NULL CHECK (length(base_sha256) = 64),
    digest        TEXT NOT NULL CHECK (length(digest) = 64),  -- build=<digest> in the template description
    disk_gb       INTEGER NOT NULL CHECK (disk_gb >= 1),
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    removed_at    TEXT,
    PRIMARY KEY (image, version),
    CHECK ((status = 'building') = (finished_at IS NULL)),
    CHECK ((status = 'removed') = (removed_at IS NOT NULL))
) STRICT, WITHOUT ROWID;

-- One build per Image at a time (D-203).
CREATE UNIQUE INDEX image_version_one_building ON image_version (image) WHERE status = 'building';

-- The history of builds stays, so numbers are never used again.
CREATE TRIGGER image_version_no_delete BEFORE DELETE ON image_version
BEGIN SELECT RAISE(ABORT, 'image versions are never deleted'); END;
CREATE TRIGGER image_version_fixed BEFORE UPDATE OF image, version, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at ON image_version
BEGIN SELECT RAISE(ABORT, 'only the status of an image version changes'); END;
-- building → ready | failed; ready | failed → removed.
CREATE TRIGGER image_version_status BEFORE UPDATE OF status ON image_version
WHEN NOT ((OLD.status = 'building' AND NEW.status IN ('ready', 'failed'))
       OR (OLD.status IN ('ready', 'failed') AND NEW.status = 'removed'))
BEGIN SELECT RAISE(ABORT, 'image version: status change not allowed'); END;

CREATE TABLE image_schedule (
    image     TEXT PRIMARY KEY,
    last_slot TEXT NOT NULL                 -- UTC time of the last planned slot
) STRICT;

CREATE TABLE plan_new (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll', 'rollback', 'import', 'ignore_rule', 'build')),
    issue_id      INTEGER REFERENCES issue (id),     -- set for revert, accept and ignore_rule
    rollback_to   INTEGER REFERENCES revision (id),  -- the revision n of a rollback (D-131)
    proposal      TEXT NOT NULL,        -- JSON: documents to put and delete
    changes       TEXT NOT NULL,        -- JSON: [{target, resources: [{resource, changes: [wire.Change]}]}]
    risk          TEXT NOT NULL CHECK (risk IN ('read', 'reversible', 'disruptive', 'destructive')),
    -- pending: waits for approval. approved: revision committed (none for
    -- origin build, D-199), run exists. stale: the head moved before
    -- approval (for build: the recipe changed).
    status        TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'stale')),
    reason        TEXT NOT NULL,
    author        TEXT NOT NULL,        -- principal
    approved_by   TEXT,                 -- principal
    created_at    TEXT NOT NULL,
    approved_at   TEXT,
    human_only    TEXT NOT NULL DEFAULT '[]',  -- JSON array of reasons (D-099, D-201)
    CHECK ((origin = 'rollback') = (rollback_to IS NOT NULL))
) STRICT;

INSERT INTO plan_new (id, base_revision, origin, issue_id, rollback_to, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at, human_only)
SELECT id, base_revision, origin, issue_id, rollback_to, proposal, changes, risk, status, reason, author, approved_by, created_at, approved_at, human_only FROM plan;

DROP TABLE plan;
ALTER TABLE plan_new RENAME TO plan;
