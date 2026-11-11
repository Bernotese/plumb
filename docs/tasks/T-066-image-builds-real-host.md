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

## Must-checks added by reviews (pl-lead, 2026-11-11)
- T-060 review S2: the dracut step of seal is not proven in CI (the test container has no dracut). On the real host, for an Ubuntu 26.04 version: `lsinitrd` of the new initrd shows no network module, and a clone with a static address takes it at the first boot, 5 of 5 boots (D-192, D-220).
- T-061 host finding B (D-222): scan the cloud-init volume of each version template: no user, hostname or vendor part of the build guest.
- T-061 host finding A (D-221): the linked-clone limit on lvmthin is documented; show the documented behaviour once.
- Thin-pool metadata: the Plan shows "unknown" (no Sys.Audit); record the text.
