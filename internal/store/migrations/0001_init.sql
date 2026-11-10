-- 0001_init: tables for the first slice (docs/ARCHITECTURE.md §3).
--
-- Conventions for all migrations:
--   * Forward only. Never edit a migration that is on main; add a new file.
--   * STRICT tables. Times are TEXT in UTC RFC 3339. JSON is TEXT.
--   * A principal is TEXT: 'user:<name>', 'token:<id>', 'agent:<target>' or 'system'.
--   * Secrets, tokens and credentials are stored only as hashes.
--   * One vocabulary: the words and ids here are the words and ids of the
--     agent protocol (internal/wire) and of the public API (D-045).
--     Revision, plan, run, issue and comment ids are integers.
--     Changes are stored as JSON arrays of wire.Change, already redacted.

-- A document, stored once by the hash of its canonical JSON body.
CREATE TABLE doc (
    hash TEXT PRIMARY KEY,              -- SHA-256 (hex) of body
    kind TEXT NOT NULL,                 -- Image | Block | Target
    name TEXT NOT NULL,
    body TEXT NOT NULL                  -- canonical JSON: {"kind","name","spec"}
) STRICT;

-- An immutable, numbered version of the state. The highest id is the head.
CREATE TABLE revision (
    id         INTEGER PRIMARY KEY,     -- revision number, starts at 1
    author     TEXT NOT NULL,           -- principal
    reason     TEXT NOT NULL,
    plan_id    INTEGER REFERENCES plan (id),   -- NULL only for the seed revision
    created_at TEXT NOT NULL
) STRICT;

-- The documents of a revision. Unchanged documents share one doc row.
CREATE TABLE revision_doc (
    revision_id INTEGER NOT NULL REFERENCES revision (id),
    kind        TEXT NOT NULL,
    name        TEXT NOT NULL,
    hash        TEXT NOT NULL REFERENCES doc (hash),
    PRIMARY KEY (revision_id, kind, name)
) STRICT, WITHOUT ROWID;

-- A proposal with its computed changes. Approval creates a revision and a run.
CREATE TABLE plan (
    id            INTEGER PRIMARY KEY,
    base_revision INTEGER REFERENCES revision (id),  -- head at plan time; NULL = empty state
    origin        TEXT NOT NULL CHECK (origin IN ('proposal', 'revert', 'accept', 'reenroll')),
    issue_id      INTEGER REFERENCES issue (id),     -- set for revert and accept
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

-- The execution of an approved plan. It starts at approval, so there is no 'pending'.
CREATE TABLE run (
    id          INTEGER PRIMARY KEY,
    plan_id     INTEGER NOT NULL UNIQUE REFERENCES plan (id),
    revision_id INTEGER NOT NULL REFERENCES revision (id),
    status      TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    started_at  TEXT NOT NULL,
    finished_at TEXT
) STRICT;

-- One step of a run: one resource on one target (guest:config included).
CREATE TABLE run_step (
    run_id      INTEGER NOT NULL REFERENCES run (id),
    seq         INTEGER NOT NULL,       -- order of creation inside the run
    target      TEXT NOT NULL,
    resource    TEXT NOT NULL,          -- resource id
    -- wire.Status values. refused: a change was above the approved risk.
    status      TEXT NOT NULL CHECK (status IN ('pending', 'running', 'unchanged', 'applied', 'failed', 'refused', 'skipped')),
    changes     TEXT NOT NULL DEFAULT '[]',   -- JSON: [wire.Change]; the action is wire.ActionOf(changes)
    output      TEXT NOT NULL DEFAULT '',
    started_at  TEXT,
    finished_at TEXT,
    PRIMARY KEY (run_id, seq)
) STRICT, WITHOUT ROWID;

-- A drift report that the user resolves. At most one open issue per target and resource.
CREATE TABLE issue (
    id          INTEGER PRIMARY KEY,
    target      TEXT NOT NULL,
    resource    TEXT NOT NULL,          -- resource id
    status      TEXT NOT NULL CHECK (status IN ('open', 'resolving', 'resolved')),
    -- JSON: [wire.Change] of the latest report; from = guest now, to = desired.
    -- The risk of the issue is the highest risk in it (the risk of a revert).
    -- Drift on 'guest:config' comes from Proxmox, all other drift from the agent.
    changes     TEXT NOT NULL,
    resolution  TEXT CHECK (resolution IN ('accept', 'revert', 'gone')),  -- gone = the drift went away by itself
    plan_id     INTEGER REFERENCES plan (id),      -- the plan that resolves it (accept, revert)
    opened_at   TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    resolved_by TEXT,                   -- principal; 'system' for gone
    resolved_at TEXT,
    CHECK ((status = 'resolved') = (resolution IS NOT NULL AND resolved_by IS NOT NULL AND resolved_at IS NOT NULL))
) STRICT;
CREATE UNIQUE INDEX issue_one_open ON issue (target, resource) WHERE status <> 'resolved';

-- A comment on an issue, from a human or an API token (tier plan or higher).
CREATE TABLE issue_comment (
    id         INTEGER PRIMARY KEY,
    issue_id   INTEGER NOT NULL REFERENCES issue (id),
    author     TEXT NOT NULL,           -- principal
    body       TEXT NOT NULL,           -- plain text
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX issue_comment_issue ON issue_comment (issue_id, id);

-- What a document is in the driver: Image → template, Target → guest.
-- This is runtime data. It is not part of the state and not of a revision.
CREATE TABLE binding (
    kind       TEXT NOT NULL,           -- Image | Target
    name       TEXT NOT NULL,
    ref        TEXT NOT NULL,           -- driver.ImageRef or driver.GuestID
    digest     TEXT NOT NULL DEFAULT '', -- Target: last seen config digest. Image: always empty
    updated_at TEXT NOT NULL,
    PRIMARY KEY (kind, name)
) STRICT, WITHOUT ROWID;

-- An enrolled agent. One agent per target.
CREATE TABLE agent (
    id              TEXT PRIMARY KEY,
    target          TEXT NOT NULL UNIQUE,
    credential_hash TEXT NOT NULL,      -- SHA-256 (hex) of the credential
    version         TEXT NOT NULL DEFAULT '',
    hostname        TEXT NOT NULL DEFAULT '',
    enrolled_at     TEXT NOT NULL,
    last_seen_at    TEXT
) STRICT;

-- A one-time enrollment token. A new token for a target replaces the old one.
CREATE TABLE enroll_token (
    target     TEXT PRIMARY KEY,
    hash       TEXT NOT NULL UNIQUE,    -- SHA-256 (hex) of the token
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    created_at TEXT NOT NULL
) STRICT;

-- The append-only event log. It is also the audit log (provenance).
CREATE TABLE event (
    seq     INTEGER PRIMARY KEY AUTOINCREMENT,   -- never reused; the SSE event id
    time    TEXT NOT NULL,
    type    TEXT NOT NULL,              -- for example 'plan.approved', 'issue.opened'
    subject TEXT NOT NULL,              -- for example 'plan:12', 'target:web1'
    actor   TEXT NOT NULL,              -- principal
    data    TEXT NOT NULL DEFAULT '{}'  -- JSON; never a secret
) STRICT;

-- A human user.
CREATE TABLE user (
    id                   INTEGER PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    password_hash        TEXT NOT NULL,          -- argon2id, PHC string
    must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1)),
    created_at           TEXT NOT NULL
) STRICT;

-- A login session of a human.
CREATE TABLE session (
    id_hash    TEXT PRIMARY KEY,        -- SHA-256 (hex) of the session id in the cookie
    user_id    INTEGER NOT NULL REFERENCES user (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
) STRICT;

-- An API token with a permission tier (docs/ARCHITECTURE.md §7).
-- Token tiers are read < plan < apply. 'admin' is the tier of a human
-- session; no token has it. Only an apply token approves, and only up to
-- its max_risk, which can never be 'destructive'.
CREATE TABLE api_token (
    id           TEXT PRIMARY KEY,      -- public part of the token
    name         TEXT NOT NULL,
    hash         TEXT NOT NULL,         -- SHA-256 (hex) of the secret part
    tier         TEXT NOT NULL CHECK (tier IN ('read', 'plan', 'apply')),
    max_risk     TEXT CHECK (max_risk IN ('reversible', 'disruptive')),  -- for tier apply, and only for it
    created_by   TEXT NOT NULL,         -- principal
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT,
    CHECK ((tier = 'apply') = (max_risk IS NOT NULL))
) STRICT;
