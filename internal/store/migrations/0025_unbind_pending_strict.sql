-- 0025_unbind_pending_strict (T-227, ROADMAP row 29; review n1 of #320).
-- (Planned as 0026 in T-215; it merged first, so it took 0025.)
--
-- * unbind_pending was the only table that is not STRICT (0023). SQLite
--   cannot make a table STRICT in place, so it is rebuilt with the same
--   columns and every row is kept (test). Nothing refers to it.
-- * Per-Target state (D-413 rule 2): unchanged. A row is the agent of a
--   deleted adopted Target (the binding is already gone): it ends when the
--   unbind is confirmed, or stays as forgotten until none of its key_ids
--   is valid (D-396). A new Target of the name, and a reused VMID, are
--   refused while it is parked (409 agent-left).

CREATE TABLE unbind_pending_new (
    target       TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL,
    guest_ref    TEXT NOT NULL,
    node         TEXT NOT NULL,
    since        TEXT NOT NULL,
    key_ids      TEXT NOT NULL DEFAULT '',  -- comma list of repair_key ids
    forgotten_at TEXT
) STRICT;

INSERT INTO unbind_pending_new (target, agent_id, guest_ref, node, since, key_ids, forgotten_at)
SELECT target, agent_id, guest_ref, node, since, key_ids, forgotten_at FROM unbind_pending;

DROP TABLE unbind_pending;
ALTER TABLE unbind_pending_new RENAME TO unbind_pending;
