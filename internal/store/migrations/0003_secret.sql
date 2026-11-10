-- 0003_secret: the encrypted secret store (D-086, D-087, D-096).
--
-- A secret row holds only AES-256-GCM ciphertext. The additional data of
-- each row binds purpose, name and version, so a row that is swapped,
-- replayed under a new version or edited fails to open. Keys are never in
-- the database; key_check holds a value derived from the master key so a
-- wrong key stops the start.

CREATE TABLE secret (
    name       TEXT PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 63),
    version    INTEGER NOT NULL UNIQUE CHECK (version >= 1),
    nonce      BLOB NOT NULL CHECK (length(nonce) = 12),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) BETWEEN 17 AND 65552), -- 1 … 65536 bytes + 16-byte tag
    updated_by TEXT NOT NULL,           -- principal
    updated_at TEXT NOT NULL
) STRICT;

-- One store-wide version counter. It never goes back and is never deleted,
-- so a deleted and new secret never gets an old version (D-087).
CREATE TABLE secret_counter (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value INTEGER NOT NULL CHECK (value >= 0)
) STRICT;
INSERT INTO secret_counter (id, value) VALUES (1, 0);

CREATE TRIGGER secret_counter_forward BEFORE UPDATE ON secret_counter
WHEN NEW.value <= OLD.value OR NEW.id <> OLD.id
BEGIN SELECT RAISE(ABORT, 'the secret version counter only goes forward'); END;
CREATE TRIGGER secret_counter_no_delete BEFORE DELETE ON secret_counter
BEGIN SELECT RAISE(ABORT, 'the secret version counter is never deleted'); END;

-- The key check value (HKDF purpose key-check/v1). Written once, at the
-- first start with a key; it never changes.
CREATE TABLE key_check (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value BLOB NOT NULL CHECK (length(value) = 32)
) STRICT;

CREATE TRIGGER key_check_no_update BEFORE UPDATE ON key_check
BEGIN SELECT RAISE(ABORT, 'the key check value never changes'); END;
CREATE TRIGGER key_check_no_delete BEFORE DELETE ON key_check
BEGIN SELECT RAISE(ABORT, 'the key check value never changes'); END;
