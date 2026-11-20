# T-248 – A failed backup job must not leave its own volume "unmarked" for ever (host finding)

- **Owner:** pl-architect (author of T-221)   · **Reviewer:** pl-reviewer (backup records = critical)   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-244 part 2, F1): when two vzdumps of one VMID overlap, Plumb's job fails, and the volume that its own vzdump still wrote stays "unmarked" for ever (`guest_job.go` records `Volume{}`). The UI text "listed when its record is complete" is wrong for it; Plumb refuses to delete it; such volumes pile up on the storage.

## Acceptance criteria
- [ ] Say first which failure paths can leave a volume written by Plumb's own task without a record (overlap, a task that ends with an error after the archive is complete, a controller stop – T-232's stop case).
- [ ] For each: either the record is completed when the volume is proven to be this job's (the PVE task id / UPID of the job, the marker, time and size), or the volume is listed as "left by a failed job of Plumb" with a delete for a human admin – never claimed as Plumb's without proof (M1/F1/F2 of #331 stay: a forged marker alone proves nothing). The text in the UI is true.
- [ ] Two jobs for one guest cannot overlap from Plumb's side (schedule vs "back up now" vs the safety backup): say what serializes them.
- [ ] Tests for each path; mutants. `make lint test`; PR to `main`; report. Host proof in T-247.

## Result (owner, max 8 lines)
branch · commit · tests · notes
