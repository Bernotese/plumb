-- 0008_agent_addrs (T-070, D-212, D-214, D-215): the address list that the
-- agent reports.
-- agent.addrs: the latest list as JSON {"list": [...], "truncated": bool};
-- NULL = unknown (old agent, bad list, nothing reported yet). Only the
-- latest list is kept: no history. The row goes away with a re-enrollment
-- and with a revoke, and the list with it.
-- agent.addrs_at: when the last stored change (a list, or unknown) arrived;
-- the controller stores at most one change per agent in 30 s.
ALTER TABLE agent ADD COLUMN addrs TEXT CHECK (addrs IS NULL OR (json_valid(addrs) AND json_type(addrs) = 'object'));
ALTER TABLE agent ADD COLUMN addrs_at TEXT;
-- issue.reopened_at: a guest:address Issue that a new mismatch opened again
-- within 1 hour after it was gone; it stays open for at least 1 hour after
-- this time (D-215, no flapping).
ALTER TABLE issue ADD COLUMN reopened_at TEXT;
