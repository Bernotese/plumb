# T-214 – Prep: three host facts for the slice 7 plan

- **Owner:** pl-backend (the one agent on the host)   · **Reviewer:** pl-architect (uses it)   · **QA:** –
- **Size:** S (facts only)   · **Depends on:** –

## Goal
The slice 7 draft (`docs/plans/slice-7-draft.md` on branch `pl-architect/slice-7-draft`, lines "FACT 1–3") needs three facts from PVE 9.2 before the contract is written.

## Allowed host actions
Read-only only: `pvesh get`, `pvesh usage <path> --verbose` (API schema), `pveum … list`, `cat` of config files, API GETs with the Plumb token, reading the PVE Perl sources on the node (`/usr/share/perl5/PVE/…`). **No vzdump, no ACL or role change, no new storage, no guest.** What cannot be shown read-only is written as "to prove in the slice 7 acceptance".

## Acceptance criteria
- [ ] FACT 1: does `GET /nodes/<n>/storage/<s>/content` return the `notes` of a backup volume; which privilege does `notes-template` (and `protected`) of `POST /vzdump` need (schema + source: is it root-only or `Datastore.Allocate`-bound?); the fallbacks named in the draft.
- [ ] FACT 2: may the Plumb token read `GET /storage/<id>` (which privilege), and what in it tells a PBS with client-side encryption from a plain dir/NFS.
- [ ] FACT 3: `mode=snapshot` of a running VM without the QEMU guest agent: what PVE does (no fs-freeze = crash-consistent), from the docs/source; LXC snapshot mode on `local-lvm`.
- [ ] Also for part A (D-379): the exact ACL path and privilege for a NIC on another bridge and for a VLAN tag on PVE 9.2 (`/sdn/zones/localnetwork/<bridge>[/<tag>]`, `SDN.Use`), and what making an isolated `vmbr9` without a port needs (file + reload command) – facts only, do not make it.
- [ ] `docs/plans/host-facts-slice-7.md` on your branch (≤ 60 lines, no address, MAC, token id), pushed, no PR. Report to `pl-lead` (max 8 lines).

## Out of scope
- Any change on the host.

## Result (owner, max 8 lines)
branch · commit · notes
