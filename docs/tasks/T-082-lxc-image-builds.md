# T-082 – LXC image builds

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-079, T-080
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
An `lxc` Image can have a build recipe; no enrollment token can reach a template.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §5 (`seal`), §11 (row T-082), D-234

## Acceptance criteria
- [ ] D-234 and review S8: no `env` and no pending `env` before the template call; a clone config holds no inherited `env`; `ssh-keygen` before ssh.
- [ ] Tests: `make lint test build` passes.
- [ ] New decisions go to `docs/decisions/T-082.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- –

## Plan (pl-backend, 2026-11-12)
Spec: D-234 + review S8. Base: PR C (#169) head; merge main after it lands.

**Contract / validation**
- `build` allowed for `type: lxc`. Build guest: as a Target (no `user`; `disk` ≥ 2). Open: `upgrade` (Q2) and `ssh_keys` of Targets from a built `lxc` Image (Q1).

**Driver (`internal/driver/proxmox`)**
- `CreateBuild(lxc)`: `Base` is the repacked file (FetchImage lxc). Create the CT with the build name `plumb-build-<image>-v<N>` as hostname, the build marker in the description, `onboot=0`, `env` token, nesting; no SSH keys. Start. Half-made cleanup as `createCT`.
- `MakeImage(lxc)`: guard as VM (range, pool, build name + marker, no template, no lock, stopped). Then `ClearBootstrap` (stopped CT: direct delete), read `config`, `config?current=1` **and** `GET …/pending`: no `env` and no pending `env` (S8), else refuse. One write with the digest: version hostname `plumb-img-<image>-v<N>`, version description (D-198 lines), delete `nameserver`/`searchdomain`/`onboot`. Then `POST …/lxc/<id>/template`. `afterFailedConversion` for CTs.
- `FindImage`, `ListImages`, `ListBuilds`, `RemoveBuild`, `RemoveImage`: CT templates and build CTs next to VMs (D-221 holds: the token sees no linked clone, C6).
- `Create(lxc)` from a version (`Image` = a VMID): full clone (`pool`, `hostname`, `storage`), check that the clone config has no `env` (S8: nothing inherited), then one config write with cores, memory, swap, `net0`, DNS (D-187), description (image marker, version, template marker), `onboot=1`, `features=nesting=1` and its own `env`; start. No `ssh-public-keys` at clone (Q1).

**Agent (`internal/agent`, seal in a container, D-234)**
- `SealBody.Container` from the engine (the build kind), so the agent needs no guess.
- Skip: cloud-init, dracut (initramfs), fstrim. Keep: upgrade check (Q2), log stop, host keys, build user (none), seed, logs.
- Container steps: `/etc/machine-id` emptied (not `uninitialized`); SSH host keys removed; a first-boot unit `plumb-ssh-hostkeys.service` (`ssh-keygen -A`, `DefaultDependencies=no`, `After=local-fs.target`, `Before=ssh.service ssh.socket sshd-keygen.service sockets.target`, wanted by `sysinit.target`, so no ordering cycle with the socket).
- `finishSeal` in a container keeps the first-boot installer of ctpack (`/etc/plumb-agent/{agent.json,ca.pem}`, `plumb-agent-install` + unit + wants link), so a clone fetches the agent at its first boot; it removes the agent binary, `plumb-agent.service` + wants link, the state dir and the credential (D-206 for VMs: no agent in a template; here: no agent binary and no credential).

**Engine (`internal/plan`)**
- Build of an `lxc` Image: base = `FetchImage(lxc)` (image:fetch), `CreateBuild`, wait hello, Blocks, `seal` with `Container: true`, wait stopped, `MakeImage`. Retention D-232 for CT versions; template files stay (D-233).
- A Target from a built `lxc` Image: create from the newest version (D-200); `guest:bootstrap` clear as today.

**Tests:** pvefake for CT clone/template/pending; driver unit tests for S8 (env in config, env pending, env inherited → refused); agent seal tests in a container root (machine-id empty, unit file + link, installer kept, binary gone); engine flow with the fake (build → version → Target).

**Host part (needs the go of pl-lead, one at a time):**
- H-B1 (token, recorded, pve tag): `CreateBuild(lxc)` from the repacked Debian 13 file, stop (the test has no agent; the CT is stopped by the token), `MakeImage` (S8 checks recorded: config, current, pending), `Create` from the version (clone has no `env` before its own write), observe, delete Target, `RemoveImage` of the version. Cleanup as T-079 (C8 leftovers, `pvesm free` of the file).
- H-B2 (real controller, branch binary in LXC 100 by a repair run, then back to main): an `lxc` Image with `build` (Debian 13, Block nginx) and one with Ubuntu 26.04; build, seal, version; a Target from each: agent enrolls, `env` cleared, `ssh-keygen` ran before ssh (journal order, `systemctl show -p ActiveEnterTimestampMonotonic`), new host keys, new machine-id, nginx answers. Delete all; template files freed by root. Or: H-B2 goes into T-083.

## Result (owner, max 8 lines)
branch · commit · tests · notes
