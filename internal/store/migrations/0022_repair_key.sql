-- 0022_repair_key (T-191; D-382, D-384, ARCHITECTURE §19).
--
-- * repair_key: the SSH repair key of this controller. The private key is
--   sealed with the secret-store key (secret.Store.SealBlob); it is not in
--   the secret table, so GET /secrets and the export never see it. At most
--   one current and one previous key (rotation, review R2 of #290).
-- * guest_host_key: the host key pins of a Target's guest, from the
--   agent's repair report over the pinned TLS link (no SSH TOFU). The
--   guest id ties a pin to one binding: a new guest has no pins.
-- * repair_report: the last checked repair report of a Target.

CREATE TABLE repair_key (
    id          TEXT PRIMARY KEY,   -- the line comment plumb-repair-<id>
    role        TEXT NOT NULL UNIQUE CHECK (role IN ('current', 'previous')),
    public_key  TEXT NOT NULL,      -- "ssh-ed25519 <base64>"
    fingerprint TEXT NOT NULL,      -- SHA256:…
    nonce       BLOB NOT NULL,
    sealed      BLOB NOT NULL,      -- the private key, sealed
    created_at  TEXT NOT NULL
) STRICT;

CREATE TABLE guest_host_key (
    target      TEXT NOT NULL,
    guest_id    TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    pinned_at   TEXT NOT NULL,
    pinned_by   TEXT NOT NULL,      -- "agent" (first report) or the approver of a repair Plan
    PRIMARY KEY (target, guest_id, fingerprint)
) STRICT;

CREATE TABLE repair_report (
    target      TEXT PRIMARY KEY,
    guest_id    TEXT NOT NULL,
    report      TEXT NOT NULL CHECK (json_valid(report)),
    reported_at TEXT NOT NULL
) STRICT;
