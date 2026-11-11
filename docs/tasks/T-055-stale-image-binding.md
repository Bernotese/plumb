# T-055 – No stale Image binding: the driver finds the template at every create

- **Owner:** tbd (proposal: pl-backend)   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M (Claude)   · **Depends on:** T-012 and T-053 on main   · **State:** planned (card only)

## Goal
A Plan that creates a guest always works when its Image document is valid. It does not matter if the Image was deleted and made again, if its checksum changed, or if someone destroyed the template on the host. Guests that exist are not touched by any of this.

## Context (read only these)
- `docs/tasks/T-015.log.md` on `origin/pl-backend/T-015-real-host` – "Real-host run 3": the defect.
- `internal/plan/run.go` – `fetchImage` (skips the driver when a binding exists), `guestStep` case `create`.
- `internal/plan/propose.go` – `guestSpec` (reads the Image binding for `spec.Image`), `createChanges`.
- `internal/driver/driver.go`, `internal/driver/diff.go` – `GuestSpec.Image`, create-only attribute `image` (D-063).
- `internal/driver/proxmox/driver.go` – `FetchImage` (finds a template by name in the pool, idempotent), `imageMarker`, `specOf`, `Create`.
- `internal/driver/fake/fake.go` – `images`, `hasImage`.
- D-014, D-063, D-077, D-098.

## Facts
1. `fetchImage` returns at once when the store has a binding `Image <name> → ref`. Nothing ever removes or checks that binding. So it is stale after: the Image document was deleted, the checksum or URL of the Image changed, or the template is gone or left the pool.
2. `Driver.FetchImage` is idempotent already (D-077): it finds the template `plumb-img-<name>-<sha[:12]>` in the pool, and it makes one when there is none. It reuses the import file. The binding is a cache of an answer that costs two API calls.
3. The create-only check of `image` compares the template VMID in the guest description with the current binding. If the binding gets a new VMID, every guest of the old template fails every Plan with "`image` can only be set when the guest is created", and its drift check reports `image` as destructive drift. So "clear the binding and fetch again" alone would break the guests that exist.

## Plan (architect)

### Rule (D-188, D-189)
- **No Image binding.** The store keeps no `Image → template` link. `guestStep` case `create` always calls `Driver.FetchImage` with the Image document of the Run's revision and gives the answer to `Create`. The driver decides: reuse the template, or import again (download only when the import file is gone too; the checksum is pinned, so it is the same file).
- **`image` is compared by the Image name.** `GuestSpec` gets `ImageName` (the name of the Image document). It is the create-only attribute `image`. `GuestSpec.Image` (the driver ref) is only the clone source for `Create`; `Diff` and `DriftGuest` ignore it and `Observe` leaves it empty.
- The Proxmox driver writes `plumb-image: <Image name>` into the guest description and reads it back into `ImageName`. The fake does the same.
- A change of the checksum or URL of an Image makes a new template at the next create. Guests that exist show no change and no drift (D-014).
- Plumb never removes a template or an import file. That stays (D-077), until the retention of T-039 (D-098).
- The Plan shows `image: <name>` in the create change of `guest:config`. It does not say if a download is needed: only the Run knows, because the template can go between plan and apply. A fetch needs no approval of its own: the approved Image document pins the checksum.

### Steps
1. `internal/driver/driver.go`, `diff.go`: add `GuestSpec.ImageName`; `createOnlyValue(AttrImage)` returns it. Update the comments of `Image` and `ImageRef`.
2. `internal/driver/proxmox/driver.go`: `configure` writes the marker with `spec.ImageName`; `specOf` reads it into `ImageName`. `Create` still checks the template with `own()` before the clone. `checkSpec` refuses an empty `ImageName` or one with a line break.
3. `internal/driver/fake/fake.go`: store and report `ImageName`; add a test helper that removes an image (the "destroyed outside" case). `Create` still fails for an unknown ref.
4. `internal/plan/propose.go`: `guestSpec` sets `ImageName = r.Image` and reads no binding. Remove the `unfetched:` placeholder. `createChanges` lists `image`.
5. `internal/plan/run.go`: `fetchImage` returns the ref and always calls the driver; case `create` sets `spec.Image`. Read the Image document from the revision of the Run, not from the head. Log one line (`image ready`, name, ref) at info level.
6. `internal/store/migrations/0006_*.sql`: `DELETE FROM binding WHERE kind = 'Image'`. No other schema change.
7. `internal/issue/issue.go`: the accept refusal for `image` stays; check that its text reads well with names.
8. `docs/ARCHITECTURE.md` §5: two sentences (no Image binding; `image` is the Image name).

### Tests
- `internal/plan` with `driver/fake`:
  - Plan A creates an Image and a Target, Plan B deletes both, Plan C creates both again with the same Image name → the Run succeeds.
  - The image is removed in the fake after the first guest. A second Target with the same Image → `FetchImage` is called again, the Run succeeds. The first Target: no drift report, an unrelated Plan (cores) works.
  - The Image checksum changes → a new guest gets the new ref; the old guest has no create-only error and no drift.
  - A Target whose `spec.image` changes to another Image → still refused as create-only, and the text names both Image names.
  - The migration removes Image rows and keeps Target rows.
- `internal/driver/proxmox` with the fake PVE (`pvefake_test.go`):
  - Template deleted in the fake → `FetchImage` makes a new template and sends no `download-url` (the import file is there).
  - Template and import file deleted → one `download-url` with the checksum.
  - Template moved out of the pool → a new template in the pool; the foreign VM gets no call.
  - `Create` then `Observe` round trip of `ImageName`; a description without the marker gives an empty `ImageName`.
- Real host (the T-015 acceptance run, same branch rules): run 3 again with the stable Image names. The template of the earlier run is destroyed as root by the test cleanup; the next Plan must succeed and must not download again.

## Acceptance criteria
- [ ] The run-3 sequence of the T-015 log passes on the fake PVE and on the real host.
- [ ] No code reads or writes a binding of kind `Image`.
- [ ] A re-made or changed template never causes drift or a create-only error on a guest that exists.
- [ ] A changed `spec.image` of a Target with a guest is still refused.
- [ ] Tests: `make lint test build` passes; `make test-pve` passes on the test host.

## Out of scope
- Removal of templates and import files (retention, T-039). After an Image is deleted, its template and import file stay on the host.
- Guests made before this change: none exist outside tests. Their description holds a VMID, so they would need a new guest.
- A Plan line "will download".

## Decisions
`docs/decisions/T-055.md` (D-188, D-189). User decision: none. One effect to tell the user: Plumb does not clean up templates and import files yet.

## Result (owner, max 8 lines)
branch · commit · tests · notes
