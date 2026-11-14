-- 0024_binding_gen (T-209, host finding F10; D-384, D-396).
--
-- * binding.gen: the generation of a binding. A new binding, and a binding
--   whose ref changes, gets the next number of binding_seq; it never
--   repeats, also after a delete. Host key pins and repair reports belong
--   to one generation (guest_id = '<ref>@<gen>'), so a reused Target name
--   or a reused VMID starts without pins.
-- * guest_host_key and repair_report: the rows of a deleted Target, or of
--   an older guest of a Target, are removed (the host has such rows); the
--   rest is tagged with the generation of its binding.

CREATE TABLE binding_seq (
    id   INTEGER PRIMARY KEY CHECK (id = 1),
    next INTEGER NOT NULL
) STRICT;

ALTER TABLE binding ADD COLUMN gen INTEGER NOT NULL DEFAULT 0;

UPDATE binding SET gen = (SELECT count(*) FROM binding b2
    WHERE b2.kind < binding.kind OR (b2.kind = binding.kind AND b2.name <= binding.name));

INSERT INTO binding_seq (id, next) VALUES (1, (SELECT COALESCE(MAX(gen), 0) + 1 FROM binding));

DELETE FROM guest_host_key WHERE NOT EXISTS (
    SELECT 1 FROM binding b WHERE b.kind = 'Target' AND b.name = guest_host_key.target AND b.ref = guest_host_key.guest_id);
UPDATE guest_host_key SET guest_id = guest_id || '@' ||
    (SELECT b.gen FROM binding b WHERE b.kind = 'Target' AND b.name = guest_host_key.target);

DELETE FROM repair_report WHERE NOT EXISTS (
    SELECT 1 FROM binding b WHERE b.kind = 'Target' AND b.name = repair_report.target AND b.ref = repair_report.guest_id);
UPDATE repair_report SET guest_id = guest_id || '@' ||
    (SELECT b.gen FROM binding b WHERE b.kind = 'Target' AND b.name = repair_report.target);
