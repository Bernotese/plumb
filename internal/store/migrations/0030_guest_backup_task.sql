-- T-248: the Proxmox task (UPID) of a guest backup job, so a job that
-- fails or is cut off can still prove its own volume later (host finding
-- F1 of T-244: an overlapping vzdump left Plumb's volume unmarked).
-- check_task = 1: the job ended without a recorded volume and its task
-- has not been checked yet (internal/backup Guests.settle).
ALTER TABLE guest_backup_run ADD COLUMN upid TEXT NOT NULL DEFAULT '' CHECK (length(upid) <= 256);
ALTER TABLE guest_backup_run ADD COLUMN check_task INTEGER NOT NULL DEFAULT 0 CHECK (check_task IN (0, 1));
