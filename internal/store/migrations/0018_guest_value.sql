-- 0018_guest_value (T-148; D-331, D-337, D-352, D-353). The copy rule.
--
-- guest_value: a keyed hash (HMAC-SHA256, own key label) of every value,
-- and of every element of a list value, that a guest reported as drift.
-- One row per (target, resource, attr, hash); a repeat report only moves
-- last_seen. issue_id is the Issue of the report when there was one (a
-- Rule can cover drift without an Issue). Rows are never evicted: at the
-- cap a report stores nothing new and flags the Target (guest_value_flag).
-- A daily prune removes rows 30 days after last_seen when the resource
-- has no open Issue.
CREATE TABLE guest_value (
    target     TEXT NOT NULL,
    resource   TEXT NOT NULL,
    attr       TEXT NOT NULL,
    hash       TEXT NOT NULL,           -- lower-case hex
    issue_id   INTEGER,                 -- no foreign key: a hint for the Plan view
    first_seen TEXT NOT NULL,
    last_seen  TEXT NOT NULL,
    PRIMARY KEY (target, resource, attr, hash)
) STRICT;
CREATE INDEX guest_value_hash ON guest_value (hash);

-- guest_value_flag: the stored values of the Target reached the cap. Until
-- "until", every Plan that touches the Target is human-only and an Issue
-- agent:values is open.
CREATE TABLE guest_value_flag (
    target     TEXT PRIMARY KEY,
    flagged_at TEXT NOT NULL,
    until      TEXT NOT NULL
) STRICT;

-- plan_guest_data: what the copy rule found for a Plan, at creation and at
-- approval. issues: JSON [id]; matches: JSON [{issue_id?, target,
-- resource, attr, element?}].
CREATE TABLE plan_guest_data (
    plan_id INTEGER PRIMARY KEY REFERENCES plan (id),
    issues  TEXT NOT NULL,
    matches TEXT NOT NULL
) STRICT;
