# T-091 – Agent self-update with auto-revert

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** T-090   · **Parent:** T-089 (C2)

## Goal
A Plumb agent can update itself on command and comes back on the old binary when the new one fails.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-091.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] Command `update`: fetch `wire.PathBinary` from the pinned controller, check SHA-256, self-test of the new binary, atomic swap, keep `.prev`, update marker, exit for a systemd restart (D-258)
- [ ] In the guest: crash loop or no successful `hello` within 5 min restores `.prev` and restarts
- [ ] `hello` sends the capability list (D-259)
- [ ] CI guest test (Debian 13, Ubuntu 26.04, VM and LXC image): good update; a binary that passes the self-test but cannot connect comes back as `.prev`
- [ ] Doc of the one-time manual step for agents from before this card (D-258, Question 6): `pct exec` for an LXC, SSH or console for a VM
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- Controller side (T-092)
- No URL field in the command; no new token right

## Plan (architect, optional)

### Design (pl-backend, 2026-11-12)
Facts it builds on: the unit is the same in VM and LXC (`Restart=always`, `RestartSec=10`; at 10 s no start limit is reached), and the unit file of older guests cannot change. A process that the agent starts dies with the agent's cgroup at the restart. So the guard is its own unit, and the guard code is the **old, known-good binary** (`.prev`), never the new one.

**Paths** (all new ones go into the `wire` agent paths/units, so no resource can touch them):
`/usr/local/bin/plumb-agent` (current) · `/usr/local/bin/plumb-agent.prev` (kept after every swap) · `/usr/local/bin/.plumb-agent.update` (temp, same FS for the rename) · `<state>/update.json` (marker) + `<state>/update.lock` (flock) · `/etc/systemd/system/plumb-agent-guard.{service,timer}`.

**`update` command (old agent, D-258 order)**
1. Refuse when a marker is `pending` ("an update is in progress"). Same SHA-256 as the running binary → `unchanged`, no restart.
2. GET `wire.PathBinary` with the pinned client; stream to the temp file (0700) through SHA-256; size cap 256 MiB; fsync. Hash ≠ command hash → `failed`, temp removed, nothing changed.
3. Self-test: `.plumb-agent.update self-test -config <cfg>` with a 30 s timeout, empty env, stdout capped. It must exit 0 and print JSON `{version, capabilities}` (loads the config, reads the state dir, no write, no network). Fail → `failed`, nothing changed.
4. Swap: hard link current → temp → rename over `.prev`; rename temp → `plumb-agent`; fsync the dir. Marker `{state: pending, from_version, from_sha, to_sha, ticks: 0}` written atomically.
5. Install and start the guard (unit + timer, `enable --now`). Then the result `applied`; after the controller took it (or after 60 s), `Run` returns `ErrRestart`, main exits, systemd starts the new binary after 10 s.
- A failure in 4 or 5 restores the old file at once and answers `failed`.

**Guard** (`plumb-agent.prev guard`, oneshot; timer `OnActiveSec=20s OnUnitActiveSec=20s OnBootSec=20s AccuracySec=1s`; enabled, so it survives a reboot)
- flock; read the marker. No marker or `confirmed` → remove the guard units and exit.
- `pending`: ticks++. Revert when ticks × 20 s ≥ 5 min (a tick count, not the wall clock: a clock jump at boot does not matter), or when `NRestarts` of `plumb-agent.service` grew by ≥ 3 since the first tick (crash loop; new base after a reboot, by boot id).
- Revert: copy `.prev` → temp → rename over `plumb-agent`; marker `reverted`; `systemctl restart plumb-agent`; remove the guard units.

**New agent** at start: marker `pending` and own SHA = `to_sha` → after the first accepted `hello` it takes the flock, sets `confirmed`, removes the guard units and the marker. Own SHA ≠ `to_sha` → it leaves it to the guard.

**Old agents (before T-091):** root, once per guest: LXC `pct exec <vmid> -- sh -c 'systemctl stop plumb-agent.service && /usr/local/sbin/plumb-agent-install'`; VM the same command over SSH or the console. The credential and the state stay. The doc goes to ARCHITECTURE §5 and STATUS.

**Tests**
- Unit (`internal/agent`, fake root, fake systemctl runner): hash mismatch, self-test fail/timeout/garbage, swap + `.prev`, rollback of a half swap, refusal while pending, same hash no-op, guard decisions (confirmed, deadline, crash loop, reboot new base, flock race with confirm).
- Guest (`make test-guest`, Debian 13 + Ubuntu 26.04, real systemd + the real unit `ctpack.AgentUnit`): `run.sh` builds v1, v2, v2-noconnect with the build tag `plumbtest` (short window: 5 s ticks, 40 s deadline; `plumbtest_noconnect` breaks the link after the self-test). Cases: good update (hello v2, guard gone, `.prev` = v1); no connect → back to v1, marker `reverted`; crash loop → fast revert; hash mismatch. Each case with the VM config (token file) and the LXC config (`token_env`, ctpack first-boot state).

**Needs the wire contract (T-090):** update body and result item id, `Hello.Capabilities`, and (proposal) a `Hello` field "last update reverted (from, to)", so T-092 can show a revert instead of a timeout. The wire agent-path additions above are small; I can do them in this PR.

## Result (owner, max 8 lines)
branch · commit · tests · notes
