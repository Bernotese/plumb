-- 0026_issue_target_deleted (T-227, ROADMAP row 31; #314 known low).
--
-- * issue.target_deleted: 1 when the delete of its Target resolved the
--   Issue (D-397 sweep). ReopenGoneIssue (D-215) never opens such an Issue
--   again, also not when a new Target of the same name reports the same
--   resource within the reopen window: the drift belonged to the old
--   guest.
-- * Per-Target state (D-413 rule 2): the column belongs to the Issue row;
--   a Target delete sets it, an unbind is a delete (the same sweep), and a
--   reused VMID or name gets new Issues.

ALTER TABLE issue ADD COLUMN target_deleted INTEGER NOT NULL DEFAULT 0 CHECK (target_deleted IN (0, 1));

-- Targets deleted before this upgrade (review low of #324): every
-- resolved Issue of a name that the head revision does not hold is marked.
UPDATE issue SET target_deleted = 1
WHERE status = 'resolved'
  AND target NOT IN (SELECT name FROM revision_doc
                     WHERE kind = 'Target' AND revision_id = (SELECT MAX(id) FROM revision));
