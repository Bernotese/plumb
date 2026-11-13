-- 0016_foreign_guest (T-126; D-322, review round 2 of #224).
--
-- foreign_guest: after a restore, the bound VMID of a Target holds a
-- guest of another name (the old controller deleted ours and Proxmox
-- gave the VMID to a new one). Set by the confirm check and by the guest
-- poll in restored mode; cleared when the name matches again or the
-- binding goes. No Issue, ignore or Rule action changes it: Plumb never
-- writes to a marked VMID.

CREATE TABLE foreign_guest (
    target    TEXT PRIMARY KEY,
    ref       TEXT NOT NULL,  -- the VMID of the binding when it was marked
    marked_at TEXT NOT NULL
) STRICT;
