-- 0007_waiting (T-056, D-195): the state "restart needed".
-- binding.waiting: values of cores and memory that are set in Proxmox and
-- wait for the next stop and start of the VM, as a JSON list of
-- {attr, from, to, since}. Only Target rows use it.
ALTER TABLE binding ADD COLUMN waiting TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(waiting) AND json_type(waiting) = 'array');
-- The stored digests already cover pending values, so without this the
-- first compare of a guest would wait for an unknown time. With it, the
-- first poll round after the upgrade compares every guest once (review S7).
UPDATE binding SET digest = '';
-- run.waits_for_restart: the number of changes that the steps of the Run
-- recorded with waits_for_restart (set when the Run ends).
ALTER TABLE run ADD COLUMN waits_for_restart INTEGER NOT NULL DEFAULT 0 CHECK (waits_for_restart >= 0);
