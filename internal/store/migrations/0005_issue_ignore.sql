-- 0005_issue_ignore (D-096): the ignore actions of slice 2 (T-032).
--
-- * Issue resolution 'ignore' (D-091, D-092). SQLite cannot change a CHECK,
--   so the table is rebuilt; the runner turns foreign keys off and checks
--   them before the commit (D-115). Ids stay, so plan.issue_id and
--   issue_comment.issue_id keep their references.
-- * issue_ignore: the memory of "Ignore once" (D-091).
-- * ignored_drift: what a Rule dropped at intake (D-092). No values.
-- * plan.human_only: why only a human may approve a Plan (D-099, D-145).

CREATE TABLE issue_new (
    id          INTEGER PRIMARY KEY,
    target      TEXT NOT NULL,
    resource    TEXT NOT NULL,          -- resource id
    status      TEXT NOT NULL CHECK (status IN ('open', 'resolving', 'resolved')),
    -- JSON: [wire.Change] of the latest report; from = guest now, to = desired.
    -- The risk of the issue is the highest risk in it (the risk of a revert).
    -- Drift on 'guest:config' comes from Proxmox, all other drift from the agent.
    changes     TEXT NOT NULL,
    -- gone = the drift went away by itself; ignore = Ignore once, or a Rule (D-091, D-092)
    resolution  TEXT CHECK (resolution IN ('accept', 'revert', 'gone', 'ignore')),
    plan_id     INTEGER REFERENCES plan (id),      -- the plan that resolves it (accept, revert, ignore_rule)
    opened_at   TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    resolved_by TEXT,                   -- principal; 'system' for gone
    resolved_at TEXT,
    CHECK ((status = 'resolved') = (resolution IS NOT NULL AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL))
) STRICT;

INSERT INTO issue_new (id, target, resource, status, changes, resolution, plan_id, opened_at, updated_at, resolved_by, resolved_at)
SELECT id, target, resource, status, changes, resolution, plan_id, opened_at, updated_at, resolved_by, resolved_at FROM issue;

DROP TABLE issue;
ALTER TABLE issue_new RENAME TO issue;
CREATE UNIQUE INDEX issue_one_open ON issue (target, resource) WHERE status <> 'resolved';

-- "Ignore once" (D-091): a later report of this target and resource with
-- exactly these changes opens no Issue. The row goes when the resource
-- reports unchanged or a Run applies it. changes is the redacted JSON of
-- the ignored Issue, in the canonical order of issue.CanonChanges.
CREATE TABLE issue_ignore (
    target     TEXT NOT NULL,
    resource   TEXT NOT NULL,
    changes    TEXT NOT NULL,
    issue_id   INTEGER NOT NULL REFERENCES issue (id),
    created_at TEXT NOT NULL,
    PRIMARY KEY (target, resource)
) STRICT, WITHOUT ROWID;

-- Drift that a Rule dropped at intake (D-092): per target, resource and
-- rule the attributes and the last time. Never a value. A row goes when
-- the resource reports unchanged; rows of removed rules are not shown.
CREATE TABLE ignored_drift (
    target    TEXT NOT NULL,
    resource  TEXT NOT NULL,
    rule      TEXT NOT NULL,
    attrs     TEXT NOT NULL,            -- JSON: [attr], sorted
    last_seen TEXT NOT NULL,
    PRIMARY KEY (target, resource, rule)
) STRICT, WITHOUT ROWID;

-- JSON array of reasons (D-099): 'rule-change' (T-032), 'secret-binding' (T-029).
ALTER TABLE plan ADD COLUMN human_only TEXT NOT NULL DEFAULT '[]';
