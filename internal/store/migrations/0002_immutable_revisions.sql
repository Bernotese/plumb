-- 0002_immutable_revisions: revisions and their documents never change
-- (docs/ARCHITECTURE.md §3). The database refuses every UPDATE and DELETE
-- on them, so no code path can rewrite history. A later retention feature
-- must replace these triggers in its own migration.

CREATE TRIGGER doc_no_update BEFORE UPDATE ON doc
BEGIN SELECT RAISE(ABORT, 'doc rows are immutable'); END;
CREATE TRIGGER doc_no_delete BEFORE DELETE ON doc
BEGIN SELECT RAISE(ABORT, 'doc rows are immutable'); END;

CREATE TRIGGER revision_no_update BEFORE UPDATE ON revision
BEGIN SELECT RAISE(ABORT, 'revisions are immutable'); END;
CREATE TRIGGER revision_no_delete BEFORE DELETE ON revision
BEGIN SELECT RAISE(ABORT, 'revisions are immutable'); END;

CREATE TRIGGER revision_doc_no_update BEFORE UPDATE ON revision_doc
BEGIN SELECT RAISE(ABORT, 'revisions are immutable'); END;
CREATE TRIGGER revision_doc_no_delete BEFORE DELETE ON revision_doc
BEGIN SELECT RAISE(ABORT, 'revisions are immutable'); END;

-- Documents join only the newest revision, which the commit transaction
-- has just created. Older revisions cannot gain documents.
CREATE TRIGGER revision_doc_only_head BEFORE INSERT ON revision_doc
WHEN NEW.revision_id <> (SELECT MAX(id) FROM revision)
BEGIN SELECT RAISE(ABORT, 'documents join only the newest revision'); END;
