# T-123 – README.md for users

- **Owner:** pl-frontend   · **Reviewer:** pl-reviewer   · **QA:** –
- **Size:** S   · **Depends on:** – (backup part: after slice 4, T-122)

## Goal
A `README.md` in the repo root for people who use Plumb (user order, 2026-11-13). English, short plain sentences.

## Context (read only these)
- `docs/BRIEF.md` §1–§5 (what Plumb is), `install/install.sh --help` and the installer parts of `docs/ARCHITECTURE.md` (only to get the facts right)
- The web UI as it is on main (screens: Images, Targets, Blocks, Plans, Issues, Settings)
- `docs/STATUS.md` "Open for the user" (known limits)

## Acceptance criteria
- [ ] Content, in this order: what Plumb is · requirements · install with `install.sh` on Proxmox (dry run first; the recovery key and what to do with it) · first login and first steps (Image, Target, Blocks, approve a Plan) · updates and the repair run (incl. the one-time root command for agents from before slice 3) · backup/restore: one line "comes with the next release" now; the full part is added after T-122 · known limits (e.g. Ubuntu 26.04, the 5 min agent guard)
- [ ] Every command and flag in it exists on main; check each against the code or `--help`. No promise of a feature that is not on main
- [ ] No development docs: no architecture, no agent-team process, no build from source, no decisions. At most short links into `docs/`
- [ ] No addresses, tokens, host names or host data; examples use placeholders like `<proxmox-host>`
- [ ] Token rights: say in plain words what the Plumb token may do on Proxmox and what it may not
- [ ] Readable on GitHub (headings, short code blocks); aim ≤ ~200 lines; `make lint` green
- [ ] Screenshots only if they show no host data; optional

## Out of scope
- Changes to code or to other docs

## Result (owner, max 8 lines)
branch · commit · tests · notes
