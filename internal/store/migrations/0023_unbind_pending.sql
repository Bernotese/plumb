-- 0023_unbind_pending (T-205; D-396, D-388, ARCHITECTURE §19).
--
-- * unbind_pending: the agent of a deleted adopted Target whose unbind was
--   not confirmed. Its credential stays valid, but the link lets it do one
--   thing only: take unbind and send its result. The row goes when the
--   result is confirmed, or when an admin forgets the guest's agent.
-- * run.warnings: the steps of a Run that ended `warning` (an applied step
--   that left something for a person, D-396), so no list shows a clean
--   success.

CREATE TABLE unbind_pending (
    target    TEXT PRIMARY KEY,
    agent_id  TEXT NOT NULL,
    guest_ref TEXT NOT NULL,
    node      TEXT NOT NULL,
    since     TEXT NOT NULL
);

ALTER TABLE run ADD COLUMN warnings INTEGER NOT NULL DEFAULT 0;
