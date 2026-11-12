# T-095 – Starter Blocks ssh-hardening, common-tools, shell, docker-compose

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-093   · **Parent:** T-089 (C5)

## Goal
Four new starter Blocks so a typical homelab guest can be built.

## Context (read only these)
- `docs/tasks/T-089-slice-3-plan.md` – the approved plan: "Risks" and the decisions D-256 … D-262 (also in `docs/DECISIONS.md`)
- `docs/ARCHITECTURE.md` – only the sections you need
- New decisions: `docs/decisions/T-095.md`; ask pl-lead for numbers (ask pl-lead)

## Acceptance criteria
- [ ] `ssh-hardening`, `common-tools`, `shell`, `docker-compose` in `blocks/`, valid against `schema/block.json`
- [ ] `docker-compose`: Docker's official apt repo with the key pinned in the Block; compose file as `file`; secrets only in a separate `.env` `file` resource with secret markers, mode 0600 (D-099); a `unit` runs `docker compose up -d`; supports `vm` (D-262); no `command` resource in it
- [ ] `docker compose` output is not logged in clear
- [ ] `ssh-hardening` must not lock out the Plumb SSH keys
- [ ] Guest tests on Debian 13 and Ubuntu 26.04 incl. a real compose app
- [ ] `make lint test build` and `make test-guest` pass

## Out of scope
- `lxc` for `docker-compose` (only after T-100 shows it works without new rights)

## Plan (owner, pl-frontend)
Findings in real guest containers (Debian 13, Ubuntu 26.04):
- Ubuntu starts sshd by `ssh.socket` (`ssh.service` disabled); Debian by `ssh.service`. A drop-in needs a restart of `ssh.service` on both. A `service: ssh` resource always sets `enabled`, so it would change the start mode of one distro (risk: no sshd after a reboot).
- Docker's key is the same for Debian and Ubuntu (fingerprint `9DC8 5822 9FC7 DD38 854A E2D8 8D81 803C 0EBF CD88`); `trixie` and `resolute` have `docker-ce` and `docker-compose-plugin`.
- Docker in the privileged guest container fails on nested overlay; `/var/lib/{containerd,docker}` on tmpfs works (test harness only).

Blocks:
- `ssh-hardening`: drop-in `/etc/ssh/sshd_config.d/10-plumb-hardening.conf` (sorts before `50-cloud-init.conf`): pubkey yes, passwords and keyboard-interactive no, `PermitRootLogin` param (`prohibit-password` default, or `no`), `MaxAuthTries` param. No `AllowUsers`/`AllowGroups`, so every key login stays (bootstrap user, deploy-user). A `unit` path watcher runs `sshd -t` and then `systemctl try-restart ssh.service`; no resource on `ssh` itself.
- `common-tools`: fixed package list, same names on both distros, no params.
- `shell`: `bash-completion` + `/etc/profile.d/plumb-shell.sh` (EDITOR, history size and time stamps, aliases; bash only).
- `docker-compose`: `sysctl net.ipv4.ip_forward=1` (makes the Block VM-only, D-260/D-262), `apt_repository docker` with the pinned key, params `distro`/`suite`, packages, `service docker`, compose file and `.env` (0600, required secret param) in `/opt/plumb-compose-<name>/`, unit `plumb-compose-<name>.service` (oneshot `up --detach`, output to null), service with `restart_on` both files and the unit.

Tests: `blocks/` (schema, params, render on VM, refused on LXC, no marker outside `.env`, no `AllowUsers`, no `service:ssh`); agent guest tests `TestGuestStarter*` on both distros (tools + shell, ssh: password refused, key login of bootstrap user/deploy/root kept, start mode unchanged; compose: real busybox app serves the secret canary, port change restarts, canary in no report and no journal).

## Result (owner, max 8 lines)
branch · commit · tests · notes
