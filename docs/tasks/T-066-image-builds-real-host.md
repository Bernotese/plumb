# T-066 – Image builds: real-host acceptance

- **Owner:** pl-backend + pl-codex-qa (pl-lead names who is on the host)   · **Reviewer:** pl-reviewer
- **Size:** M   · **Depends on:** T-058 … T-064 on main   · **Parent:** T-039

## Goal
Image builds work on the real Proxmox host for Debian 13 and Ubuntu 26.04, and the known limit D-192 goes away.

## Context (read only these)
- `docs/tasks/T-039-image-builds.md` Plan §8 row "Real host", "Not proven on the host"; `docs/tasks/T-039.log.md`; AGENTS.md "Test host"

## Acceptance criteria (`make test-pve`, not CI; log in `docs/tasks/T-066.log.md`)
- [ ] Per distribution: build with the real Go `seal` and `MakeImage`; the power-off handshake (B1); two Targets from the version.
- [ ] Proofs C2 … C6 with the real `seal`: no word of the build guest's hostname, address or user under `/var/log`, `/root`, `/home`, `/tmp`.
- [ ] Search of the template disk: no build credential, no token, no desired list, no agent binary.
- [ ] Ubuntu from a template built **with** the upgrade (new kernel): static address at the first boot, after a reboot and after a cold start (D-192).
- [ ] A Target without SSH keys; disk growth from a built Ubuntu template.
- [ ] A build refused by the 85 % rule; retention removes a version with the token; a short schedule fires once.
- [ ] End state: 0 guests, 0 templates, 0 import files of this card.
- [ ] Then: remove the known limit D-192 from ARCHITECTURE §5.

## Out of scope
- Other distributions, LXC.

## Result (owner, max 8 lines)
branch · commit · tests · notes
