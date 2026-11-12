# T-083 – real-host acceptance of LXC Targets and QA audits

- **Owner:** pl-backend, pl-codex-qa   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** T-075 … T-081
- Child card of T-038 (plan approved, D-238). The plan is the spec: `docs/tasks/T-038-lxc-targets.md`.

## Goal
LXC Targets are proven on `pve-dev`.

## Context (read only these)
- `docs/tasks/T-038-lxc-targets.md` §10 (H1 … H11 without H10)
- AGENTS.md "Test host (Proxmox)"

## Acceptance criteria
- [ ] Every host check of §10 with its result in `docs/tasks/T-083.log.md` (no address, no token id, no key).
- [ ] From T-082 (review B1, moved H-B2): a full real LXC image build with the controller on `main`, Debian 13 and Ubuntu 26.04: build → seal → template → a Target cloned from the version boots, its agent installs and enrolls, first Run green; `ssh-keygen` runs before ssh (journal); no `env` in the template or the clone.
- [ ] The host is clean at the end.
- [ ] New decisions go to `docs/decisions/T-083.md` (ask pl-lead for numbers). Merge `origin/main` before you report.

## Out of scope
- –

## Run plan (pl-backend, 2026-11-12; needs the go of pl-lead, after PR #176 is on main)
**Rules.** Host rules of AGENTS.md. Everything Plumb does goes through its API from the dev VM (API token of `PLUMB_TEST_TOKEN_FILE`, an admin session for destructive Plans), like T-073. Root on the host only read-only, plus the root actions named below, only on Plumb's test containers (pool `plumb`, VMID range, names `t083-…`). Addresses `PLUMB_TEST_VM_IP_1/_2`, gateway and DNS come from `testhost.env` and never go into the log (`<ip1>`, `<ip2>`). A defect stops that check: report to pl-lead, no fix on the host. At most 3 containers run at a time (7 GB host). Log: `docs/tasks/T-083.log.md`.

**R0 – controller to main.** Build main, stage in `/root/plumb-install` (0700), `install.sh --dry-run` → result to pl-lead → go → repair run (`ssh -tt`, `--yes`). After: `plumb version` = main, `active`, `NRestarts=0`, start log "Proxmox host checked", no `.plumb-new`, `plumb-images` content `import,vztmpl`, DB `user_version` (0011 applied; DB copy read on the host, removed).

**Images.** `t083-d13`: lxc, `debian-13-standard_13.6-1` URL + SHA-512 of the signed index (host fact of T-079). `t083-u26`: lxc, `ubuntu-26.04-standard_26.04-1` + its SHA-512 (read from the host's index cache, read-only).

| check | how | pass when |
|---|---|---|
| H1 | Plan: Image `t083-d13`, Target `t083-d1` (static `<ip1>`, cores 1, 512 MB, disk 4), Blocks `nginx` + `motd`. API token approves (admin session if the Plan is human-only: new lxc Image, D-235). | Run green: `image:fetch`, `guest:config`, `guest:bootstrap`, resources applied; agent online; token `GET …/config` (next view) has no `env`, `/pending` shows only `delete: env`; root `pct exec … cat /proc/1/environ \| tr '\0' '\n' \| grep -c PLUMB_ENROLL_TOKEN` (count only, never the value); `curl http://<ip1>/` from the dev VM answers from Nginx. |
| H2 | Image `t083-u26`, Target `t083-u1` (DHCP), Block `motd`. | Run green; the Target status shows the DHCP address from `/interfaces`; same `env` checks as H1. |
| H3 | (a) root `pct exec <t083-d1> -- sh -c 'echo x >> /etc/motd'`; (b) root `pct set <t083-d1> --memory 768`. | (a) an Issue on `file:/etc/motd` within the drift time; (b) a `guest:config` Issue `memory 768 → 512`; Revert of (b) by a Plan → live, Issue resolved. |
| H4 | Plan: `t083-d1` cores 1 → 2, memory 512 → 768. | Run green without a restart step; `uptime` in the CT keeps counting; `nproc` = 2, `free` shows 768 MB. |
| H5 | Re-enrollment Plan for `t083-d1` (`allow_restart: true` set first). | New token in `env`, cold restart, hello, `guest:bootstrap` applied, agent online with a new credential; the old credential gets 401. |
| H8 | Block `nginx-site` on `t083-d1` (default site link). | Run green; in the CT `/etc/nginx/sites-enabled/<site>` is a link to `sites-available/<site>` (renameat2), Nginx answers for the site. |
| H7 | (a) second Target `t083-d2` from `t083-d13`; (b) root `systemctl restart plumb.service` inside LXC 100 (controller down for seconds, announced), then Target `t083-d3`; (c) after H6/H11 removed the Targets of `t083-d13`: change its pin to `debian-12-standard_12.12-1` (URL + SHA-512). | (a) and (b) no new upload: the vztmpl list of `plumb-images` keeps one file, `image:fetch` says reused; (c) the Image status lists the old file as unused with the `pvesm free` line (T-085), the new file in use. |
| H9 | Target `t083-big` with `disk` larger than the free space of `local-lvm`. | Plan or Run refused by the disk gate with its text; nothing made (pool and `pct list` unchanged). |
| H6 | Delete Plan of `t083-d2` (admin session). Before: the agent credential copied from the CT into a 0600 file (never printed). | CT gone; the credential gets 401 on poll and report (T-057 method); file shredded. |
| H11 | After H6: Target `t083-d4`. | It gets the freed VMID (C8 leftovers there) and starts; Run green. |
| B-D13 | Image `t083-bd13`: lxc, Debian 13 pin, `build` (Block `nginx`, upgrade true, disk 4). Build Run, then Target `t083-bt1` (static `<ip2>`) from v1. | Build: `image:base` (repacked file), build CT, hello, apply, seal (container steps: initramfs/trim skipped, upgrade applied, list of removed packages), stopped, template. Token: version template config + `/pending` without `env`. Target: full clone, no inherited `env`, own `env`, first boot: installer fetches the agent, enrolls, first Run green, `env` cleared. In the clone: `journalctl -b -o short-monotonic -u plumb-ssh-hostkeys.service -u ssh.service -u ssh.socket` shows the keys made before ssh starts; host keys differ from the build CT's; `/etc/machine-id` set and new. |
| B-U26 | Same with Ubuntu 26.04 (`t083-bu26`, Target `t083-bt2`, DHCP). | Same; Ubuntu uses `ssh.socket` (socket activation). |

**Open question for pl-lead:** §10 also names H12 (installer upgrade path of §7 on the running install; R0 is that path from `e295d94`) and H13 (stop `plumb.service` during a template fetch: no temp file left, controller down for seconds). In or out?

**Cleanup (end).** Delete Plans for all Targets and Images (admin session); built versions removed by Plumb; template files freed by root `pvesm free` by exact name (Plumb never deletes them, D-233); C8 leftovers of the VMIDs of this run (`rm` of `rules.seccomp`, `rm -r` of an empty `passthrough/`, `rmdir`); credential and CA copies shredded. End: `pct list` only 100, pool empty, `plumb-images` without `vztmpl` files of this run, no `pveupload-*`, controller `active`.

## Result (owner, max 8 lines)
`pl-backend/T-083-real-host` · log `docs/tasks/T-083.log.md` · all PASS on pve-dev with main `8af26b0`: R0 = H12 (upgrade, migration 0011 live), H1–H9, H11, H13, B-D13 and B-U26 (real build → seal → template without env → clone enrolls, ssh-keys before ssh). No defect. Host clean.
Notes: nginx Block default site 403 (index name), cores reach the CT after ~10 s, two old T-039 Issues open.
