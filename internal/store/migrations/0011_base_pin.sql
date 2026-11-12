-- 0011_base_pin (T-082, D-254): the base pin of an image version is the
-- SHA-256 of a vm base (64 hex) or the SHA-512 pin of an lxc base (128
-- hex, D-224). SQLite cannot change a CHECK, so image_version is rebuilt
-- with its rows, index and triggers (as plan in 0009).

CREATE TABLE image_version_new (
    image         TEXT NOT NULL,            -- Image document name
    version       INTEGER NOT NULL CHECK (version >= 1),
    status        TEXT NOT NULL CHECK (status IN ('building', 'ready', 'failed', 'removed')),
    trigger       TEXT NOT NULL CHECK (trigger IN ('change', 'manual', 'schedule')),
    run_id        INTEGER NOT NULL REFERENCES run (id),
    revision_id   INTEGER NOT NULL REFERENCES revision (id),  -- the pinned head of the Run
    recipe_sha256 TEXT NOT NULL CHECK (length(recipe_sha256) = 64),
    base_sha256   TEXT NOT NULL CHECK (length(base_sha256) IN (64, 128)),  -- vm: SHA-256, lxc: SHA-512 pin
    digest        TEXT NOT NULL CHECK (length(digest) = 64),  -- build=<digest> in the template description
    disk_gb       INTEGER NOT NULL CHECK (disk_gb >= 1),
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    removed_at    TEXT,
    PRIMARY KEY (image, version),
    CHECK ((status = 'building') = (finished_at IS NULL)),
    CHECK ((status = 'removed') = (removed_at IS NOT NULL))
) STRICT, WITHOUT ROWID;

INSERT INTO image_version_new (image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at, finished_at, removed_at)
SELECT image, version, status, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at, finished_at, removed_at FROM image_version;

-- The triggers of the old table go with it.
DROP TRIGGER image_version_no_delete;
DROP TRIGGER image_version_fixed;
DROP TRIGGER image_version_status;
DROP TABLE image_version;
ALTER TABLE image_version_new RENAME TO image_version;

CREATE UNIQUE INDEX image_version_one_building ON image_version (image) WHERE status = 'building';
CREATE TRIGGER image_version_no_delete BEFORE DELETE ON image_version
BEGIN SELECT RAISE(ABORT, 'image versions are never deleted'); END;
CREATE TRIGGER image_version_fixed BEFORE UPDATE OF image, version, trigger, run_id, revision_id, recipe_sha256, base_sha256, digest, disk_gb, started_at ON image_version
BEGIN SELECT RAISE(ABORT, 'only the status of an image version changes'); END;
CREATE TRIGGER image_version_status BEFORE UPDATE OF status ON image_version
WHEN NOT ((OLD.status = 'building' AND NEW.status IN ('ready', 'failed'))
       OR (OLD.status IN ('ready', 'failed') AND NEW.status = 'removed'))
BEGIN SELECT RAISE(ABORT, 'image version: status change not allowed'); END;
