# T-277 – RC5 host run on pve-dev (after the cleanup, D-442)

- **Owner:** pl-backend (the only agent on the host for this card)   · **Reviewer:** pl-reviewer (the run plan, then the log)   · **QA:** –
- **Size:** L   · **Depends on:** every cleanup PR merged (T-271, T-274, T-275, T-276); RC5 = `main` after them

## Goal
D-442: after the cleanup, one short host run with a new release candidate. It has five parts:
- (a) the upgrade RC4 → RC5 with the bundle check;
- (b) the checks that the cleanup changes touch;
- (c) one pass over what only tests have proven so far (`docs/plans/finished-check.md` section 2);
- (d) the C5b leak scan of RC5;
- (e) the cleanup and the end proof.

The result decides whether RC5 is the release candidate.

## Context (read only these)
- `docs/tasks/T-263-release-host-rerun.md` and `docs/tasks/T-263.log.md`: the rules, the file layout on the host, the step and proof format, R1, the RC4 step and the end proof. This card uses all of them.
- `docs/plans/finished-check.md` section 2: what is proven only by tests.
- `docs/tasks/T-266.log.md`: the method of the C5b scan.
- `docs/plans/release-day.md` (PR #407): step 3 compares the release build with the expected hashes, which this card records again (R5-0).

## Rules (hard; the same as T-263)
- **The test host rules.** `AGENTS.md` "Test host" applies in full. Do only what this card lists. For any other action, ask `pl-lead` first.
- **Installer runs.** Every installer run (update, remove, clean install, restore, repair) goes `--dry-run` → the result to `pl-lead` → the go → the real run. Announce every controller stop before it.
- **The RC build.** Build RC5 **locally** with `scripts/package-release.sh v0.1.0-rc.5 <new dir>` in a clean worktree of the RC5 commit. Delete the build output, and remove the worktree without `--force`.
  - **No tag, no GitHub release or draft, and no push of an RC.** The tarball goes to the host only by `scp`.
  - Record the commit and the four hashes (`plumb`, `plumb-agent`, `install.sh`, the tarball).
- **No changes outside the plan.** No new right. Do not change the network, `vmbr0`, the `local`/`local-lvm` definitions or the SSH setup. `vmbr9` stays (its undo is C8).
  - At the start, copy `storage.cfg` and the ACL view.
  - At the end, `storage.cfg` must be byte-equal and the ACL view equal.
- **Keys and secrets.** Real `plumb key init` runs are allowed (D-180). The recovery key goes to `~/.config/plump/testhost-recovery.key` (0600), and the log says only "key stored in <path>". Pass tokens and passwords by a 0600 file or stdin, never on a command line.
- **The log holds** no address, token id, MAC, key, fingerprint or host local time. Times are in UTC (`…Z`). Name volumes by VMID and UTC time.
- **Test objects** live only in the Plumb pool and are named `t277-*`. The test secrets file is `~/.config/plump/t277-secrets.json` (0600). It stays until the C5b scan of (d) is done, and is deleted after the cleanup (D-416).
- **The screenshots of (c1)** go to the owner's scratchpad (0700). They are **never committed**, never put in a PR and never sent anywhere. `pl-lead` looks at them, and they are deleted at the end. The log names only the file names and the browser-shot JSON lines (status, title, console errors), without page text.

## Steps
First, the run plan: `docs/tasks/T-277.log.md` on a branch, in the format of the T-263 log. It holds the ordered steps, the proof per step, the objects, each installer line without secrets, the root actions you will ask for, and the cleanup list. `pl-reviewer` checks it, and `pl-lead` says go. **No host action before that.**

| #   | Step | Proof to record |
| --- | ---- | --------------- |
| 0   | **Start state:** `storage.cfg` and the ACL view (rows, normalized); the controller version (RC4); the counts from a read-only DB copy (`user_version`, the rows per table; remove the copy after) | the copies under `/root/plumb-install/t277/start/` (0600); the counts in the log |
| a1  | **RC5 build:** the RC5 commit and the four hashes. Also the hashes of a `v0.1.0` build of the same commit (**R5-0**): they replace R4-0 as the expected release hashes, because the cleanup changed build inputs (#411, #417, …) | the commit, the 4 + 4 hashes; the worktree removed |
| a2  | **Bundle check of RC5** (short form of T-263 step 2): the good bundle passes; one damaged byte in `plumb` is refused; a set `PLUMB_INSTALL_*` variable is refused; nothing changed after the refusals | the lines "Bundle check of release v0.1.0-rc.5: SHA256SUMS matches …" and the two refusal lines; the ACL view unchanged |
| a3  | **Upgrade RC4 → RC5:** a repair run with today's flags (the state file gives them since T-269: the dry run's restore line must have no `<UNKNOWN …>`) | the dry run: `plumb version: v0.1.0-rc.4 -> v0.1.0-rc.5`, **0** `+ pveum` lines, no "older rights … removed" line. After the real run: the ACL view = step 0, the counts = step 0, `user_version` the same or higher, the Targets of step 0 ready |
| a4  | **Agent update to RC5** on every Target (one "Propose agent update" Plan each, approved by a person) | each agent's hello has the RC5 `plumb-agent` hash; the agent page says "current"; no Issue opened by the update |
| b1  | **Texts (T-274):** the UI labels are seen in (c1). One API refusal per changed group: a Plan on a Target with a Run in progress ("a Run is working on this Target"); a proposal for an unknown Target ("no such Target"); a Block whose `after` names a resource that is not on the Target ("… is not on this Target") | the HTTP code and the problem `type` and `detail` of each (the type and code are unchanged since RC4) |
| b2  | **Agent paths (T-271, `AgentConflicts`):** proposals that must be refused at render time: a `file` under `/var/lib/plumb-agent/`; a `symlink` whose target is `/etc/plumb-agent/ca.pem`; a `symlink` with the **relative** target `../lib/plumb-agent/credential`; a `symlink` `/opt/t277 → /var/lib` together with a `file` `/opt/t277/plumb-agent/x`; a `service` named `plumb-agent` | each refusal (code, type, the named resource); no Plan made; the guest unchanged |
| b3  | **Capabilities (T-271):** in a2 to a4 the RC4 agents (before their update) and the RC5 agents both report, and the update Plan is offered only where the agent can update | the agent view before and after a4; no `agent-outdated` refusal for a normal Plan |
| b4  | **Revisions (T-271):** every Plan of this run that changes the desired state stores a revision with author, reason and Plan id | `GET /revisions` rows of this run (author, reason, `plan_id`) |
| c1  | **The web UI on the real controller, through a local read-only proxy** (no change of `browser-shot` or of the VM `mobtest`, D-326). See "c1: the proxy" below. `browser-shot --insecure` opens the proxy URL and takes: Targets, one VM and one LXC Target, a Plan with its risk, a Run, the Issue inbox and one Issue, History with a diff, Settings, Backups; desktop, one at 390 px wide, one `--dark`; and the login page directly, without the proxy cookie | the file list, and per picture the browser-shot JSON (status 200, title, **0 console errors**); `pl-lead`'s "looked at" in the log; the proxy's own log: only GET/HEAD/SSE requests, every other method refused (count) |
| c2  | **Rollback to a revision:** change the `motd` parameter of `t277-vm` (revision N → N+1), then roll back to N (a Plan, a person approves) | the rollback Plan names revision N; the Run applied; `/etc/motd` on the guest has N's text again; the new revision's reason names the rollback |
| c3  | **Import:** `GET /export` (without secrets) → import the same file → a Plan with no document change, or the Plan names exactly the changed documents of a one-line edit (a Block description) | the import Plan's documents list; after the approve, the revision; no secret in the export file (grep for the test secrets' names only) |
| c4  | **The one-time-password unlock of another user after a restore.** This needs a restore of Plumb (the R1 path of T-263 step 6: backup → `--remove --keep-guests` → clean install from the RC5 tarball → `plumb restore`). **Only with `pl-lead`'s go:** it is the longest step, and it also proves R1 again on RC5. In it: a test user `t277-op` (role `plan`) made before the backup is locked after the restore; the admin sets the new password first (D-441); then the admin gives `t277-op` a one-time password in Settings → Backups; `t277-op` logs in with it, must choose a new password, and then works with role `plan` | the user list before and after; the 403 `password-change-required` before the change; `t277-op`'s login, the forced change, and one read with role `plan`; R1's seven points in short form (as RC4 in T-263) |
| c5  | **A timer unit and the `command` resource:** one test Block `t277-timer` with `unit` `t277.service` + `t277.timer` (every 5 min, writes a marker line) and one `command` resource (writes a marker file once, `creates` that file), on `t277-vm` | the Run applied; `systemctl is-active t277.timer` = active; the marker grows; a second apply does **not** run the command again (one line in the marker file); no drift Issue for the `command` resource |
| c6  | **The Blocks `shell` and `common-tools`** on `t277-vm` and `t277-ct` | the Run applied on both; `dpkg -s` of the tools = installed; the shell files present; no Issue after the next 15 min full check |
| c7  | **Ignore by rule:** a hand change of a managed file on `t277-ct` → Issue → "Ignore by rule" (this Target) → the rule Plan (human-only) approved → the same hand change again opens **no** Issue → the rule removed (a Plan) → the next drift opens an Issue again | the Issue ids, the rule document in the revision, the "no Issue" window (≥ 1 full check), the reopened Issue |
| c8  | **Adopt a file as raw:** a hand edit of the rendered `/etc/motd` on `t277-vm` that no parameter can express → the Issue offers "Accept as raw" → Plan → approve | the Issue's `accept_mode` raw; after the Run the resource is raw with the guest's text; no drift after the next full check |
| c9  | **A new Image version leaves existing Targets alone:** a built Image `t277-img` (v1) → Target `t277-iv` from v1 → a new build (v2) | after v2: `t277-iv` keeps `image_version` 1, no Plan or Issue for it, its guest config unchanged (Proxmox poll); v1 stays while `t277-iv` uses it |
| d   | **C5b of RC5** (pl-reviewer, as T-266): pl-backend puts the RC5 tarball, a read-only DB copy, the journal export and the events export into `~/.config/plump/t277-c5b/` (0700); pl-reviewer runs `scripts/leak-scan.py` (with `t277-secrets.json` and the admin password files of the day) and gitleaks `--redact` there only; places and kinds only | the counts in the log (no value); pl-reviewer's OK before (e) deletes the folder |
| e   | **Cleanup and end proof** (with `pl-lead`'s go, after d): one delete Plan of the `t277-*` Targets, Blocks and Images (checked to name only these); `t277-op` and `t277-view` disabled; tokens revoked; the proxy and the tunnel gone; the pictures deleted; `t277-secrets.json` and the C5b folder deleted (D-416) | only LXC 100 on RC5; no `t277-*` object in Plumb or Proxmox; `storage.cfg` byte-equal; the ACL view = step 0; the leak grep of the log (counts only); the log as a PR |

## c1: the proxy (`pl-lead`'s way, 2026-11-20)
- **The script:** a small reverse proxy in the owner's scratchpad, **not in the repo**. It listens only on the dev VM address that `mobtest` reaches, on a free port, and only for the minutes of the step.
- **The way in:** the proxy forwards to an SSH tunnel on the dev VM to the controller's port 8443. It checks TLS with the Plumb CA file (no `--insecure` between the proxy and the controller).
- **The session:** a read-only user `t277-view` (role `read`) gets a session by the API login (the password by a 0600 file or stdin). The proxy adds that session cookie to each request. The cookie lives only in the proxy's memory or a 0600 file, never on a command line and never in a log.
- **Read only:** the role `read` cannot approve or write. The proxy also refuses every method but GET and HEAD, and the event stream (`GET /events`) passes.
- **Right after the pictures:** the session ends (`DELETE /api/v1/session`), `t277-view` is disabled, and the proxy and the tunnel are stopped by their PIDs (never `pkill -f`). The 0600 files go.
- **If the proxy way fails for a technical reason:** fall back to the login page only, and say so in the log. A change of the shot tool in `mobtest` then becomes a question for the user (through `pl-lead`). The other screens stay proven by tests only until then.

## Out of scope
- U12 (the upgrade from the user's own version) and the `vmbr9` undo (C8): both wait for the user.
- A release, a tag, a workflow run.

## Result (owner, max 8 lines)
branch · commit · tests · notes
