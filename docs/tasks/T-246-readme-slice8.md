# T-246 – README: disks, Explain an Issue, stock Image delete, limits

## Handover (pl-frontend, 2026-11-15; paused by the budget rule)
- **Branch** `pl-frontend/T-246-readme-slice8`, pushed, **no PR**. Draft only, README + one picture.
- **In the draft (`README.md`):**
  - "First steps" → Targets: **Data disks** (slot = fixed name; VM raw disk with serial `plumb-N`, `lsblk -o NAME,SERIAL`; LXC mount path, create-only, no system tree; LXC add only cold per D-437 (running → stop, add, start, needs allow_restart; failed start → disk removed again); grow only; remove = destructive, person approves, detach then free in a second step; the "manages" box; hand-made disks per ARCH §21 H1; 85 % gate, apply may approve adds, not removes; adopted = none). Picture `docs/screenshots/readme-target-disks.png`.
  - "Guest backups" → Restore: a **backup older than a disk** (VM keeps it as unused volume, LXC loses the data; the safety backup holds it).
  - "Explain an Issue": **T-240 (#367) wrote it**; the merge kept their text. Do not add a second one.
  - "Known limits": stock Image delete (D-430), disks only on the install storage (no bind mount, no mount options), symlink in an LXC path (+ a disk added to a stopped container is first tried at its next start, the low of the #365 re-check), first NIC address create-only (D-424), PBS not tested (U9, D-434), restore of a gone guest needs a right Plumb does not take (U10, D-435).
- **Re-check before the PR:**
  - T-238 (merged code): the exact refusal and step texts (stop-add-start, failed start, `guest:volume-left`, "no further add while it is open"), the shrink error, the apply-token limit (3 × 4096 GiB); add `guest:volume-left` to the README if the user can meet it.
  - T-241 / T-242: the stock Image delete text (what the Image page lists, the kept template in the step output).
  - T-247 (host acceptance): the restore result of an older backup on the real host (VM `unused0`, LXC lost) and the stop-add-start; change the text where the host differs.
  - Then: merge `origin/main`, `make lint test`, PR to `main`, reviewer pl-architect (card), report.
- **How the picture was made (mock only):** in `web/`: `VITE_PLUMB_MOCK=1 ./node_modules/.bin/vite --host 0.0.0.0 --port 5291 --strictPort`; then a Playwright script in the VM `mobtest` (`lxc file push` + `/opt/pw/bin/python3`, `PATH` needs `/snap/bin`): log in `admin` / `correct-horse-battery-staple`, do the forced password change, `location.hash='#/targets/db1/edit'`, click `#add-disk`, fill `#disk-2-size` = 20, `page.locator("#disks").screenshot(...)` at 1280 px, light theme. Pull the PNG, remove the script and PNG from `mobtest`, stop vite by its PID (not `pkill -f`).

## Left of T-239 (disk list e2e on the real server, after T-238 merges)
- New spec `web/e2e/slice8-disks.spec.ts` (runs after `slice.spec.ts`; log in with `e2e/login.ts`; `serve --dev --dev-demo` with the fake driver, see `slice7-backups.spec.ts`).
- Steps: (1) open `#/targets/<a demo VM>/edit`, tick `#disks-managed` (the first-time note `#disks-unmanaged-vm` shows), `#add-disk`, size 20, reason, "Show the plan": the Plan has `disk1` "20 GiB", `reversible`; approve; the Run succeeds; the edit page shows slot 1 fixed. (2) Size 10 → field error "only grows"; size 30 → Plan `disk1` 20 → 30 GiB, `disruptive`. (3) Remove → banner `#disks-removed`; the Plan is `destructive` with the warning that names the volume and the second free step; a `plan`/`apply` token gets 403 on approve, a person approves. (4) An LXC demo Target: add with path `/etc/x` → field error; `/srv/data` on a running container without allow_restart → `#disks-add-error`, with it → `disruptive`. (5) Untick the box → the saved document has no `disks`, the Plan has no disk line.
- Do not push before the steps pass locally (CI runner rule); the web CI job runs e2e.

- **Owner:** pl-frontend   · **Reviewer:** pl-architect   · **QA:** T-245
- **Size:** S   · **Depends on:** T-238 … T-242 merged; T-247 passed

## Goal
Slice 8, child card **C10** of T-236. The user README covers slice 8.

## Context (read only these)
- Plan: `docs/tasks/T-236-slice-8-plan.md` section "Plan" (rows, shared-file order, rules), decisions D-421 … D-435 in `docs/DECISIONS.md`, `docs/plans/ROADMAP.md`. Read row C10, the contract parts and decisions it names, and the review comments of PR #359 on it.
- `AGENTS.md`: "Autonomy and release" (D-326), "Flow" step 5 (D-394), "Git" (remove temporary worktrees), "Test host".

## Acceptance criteria
- [ ] Everything row C10 lists; touch only the files that row gives you (test files next to them, single wiring lines, your own planned guards and marks: no question needed, list them in the PR). Another file: ask `pl-lead` first.
- [ ] User text only; the U9 limit in plain words (not tested on a real PBS) unless the user decides otherwise; no release text (D-326).
- [ ] Rules: a new command type goes through the real link in a test; every per-Target table says what happens at delete, unbind and VMID reuse; a new operation updates `roleHints.ts`, the tier test and the contract marks; a new attribute gets the guest-value classification test. Name in the report the CI job that proves the card and which of your tests skip locally.
- [ ] No new token right (D-423). Decisions only with numbers from `pl-lead`. Migrations: the next free number at merge time.
- [ ] `make lint test`; `origin/main` merged; own worktree and branch; PR to `main`; pictures only from the mock; report to `pl-lead`.

## Out of scope
- Other rows. Any release step. The user's own installation.

## Result (owner, max 8 lines)
branch · commit · tests · notes
