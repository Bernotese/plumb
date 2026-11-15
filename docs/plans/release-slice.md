# Release slice (slice 9) – DRAFT

**State: draft, nothing started.** No workflow run, no tag, no release, no repo setting. Nothing of it reaches `main` without review; pl-lead turns it into cards when slice 8 is done (D-326 point 3). Inputs: `docs/BRIEF.md` §5/§6, `docs/plans/ROADMAP.md`, `docs/STATUS.md` (2026-11-16), D-421 (what the release slice holds), D-432 … D-435, `.github/workflows/release.yml` (T-124), `scripts/package-release.sh`.

## 1. BRIEF §5/§6 after slice 8
Every line of §5.1 … §5.9 and §6 was checked in code by T-236 (ROADMAP "Re-checked by T-236"). After slice 8 only these are not proven:

| # | Brief | Point | State after slice 8 | Where |
|---|---|---|---|---|
| R1 | 5.3 | "a fresh Plumb restored from backup can reconcile everything again" | unit and API tests; last real-host proof in slice 4, before guest backups, NICs and disks existed (ROADMAP row 38) | C7 (full host re-run) |
| R2 | 5.1 | one tarball, run on a fresh host | `package-release.sh` builds it; never installed on a host from a tarball; the installer does not check `SHA256SUMS` itself | C3, C7 |
| R3 | 5.3 | secrets never in clear in plans, logs, exports | tests (T-243 log scan, response scan, leak hunts of slices 2–7) | C5 (leak hunt of history and artifacts) |
| R4 | 5.3 | backups via PBS | the encryption state is read from the storage config; never run against a real PBS (U9) | U9, C6 (docs) |
| R5 | 5.5 | disks, network as desired state | done in slice 8 (T-238, host proof T-247) | – |
| R6 | 5.8 | AI explains Issues | done in slice 8 (T-240, host proof T-247) | – |

§6 is answered in full (ARCHITECTURE §1 … §9). Everything else of §5 is done and proven on `pve-dev` (slices 1 … 8).

## 2. Open list (STATUS, ROADMAP, lows on record)
| # | Point | Source | Plan |
|---|---|---|---|
| O1 | `TestGuestFirewall` failed once in the VM job (port 8080 not reachable although the table accepts it); cause unknown, not proven test-only; T-200 adds a dump | STATUS | C9: run the VM guest job 20 times on a branch; a failure with the dump → a fix card; no failure → the release notes name it as a known unexplained single failure |
| O2 | `vmbr9` on `pve-dev` (D-379, D-432): stays for the NIC regression proof of C7, then the undo with the files on the host | STATUS, D-432 | C8 (after C7) |
| O3 | 2 low `npm audit` findings (`deepmerge` 4.3.1, dev-only, not in `web/dist`; no patched version) | ROADMAP row 19, T-155 | C2: check again; still no fix → record in the release notes, no suppression |
| O4 | GO-2026-5932 | ROADMAP row 19 | C2: `govulncheck` again; a fixed Go or module version → update |
| O5 | Ubuntu 26.04 stock image can stay on DHCP until its first restart (D-192, D-220, D-223) | ROADMAP row 19 | C6: in the README limits (already documented; check the text) |
| O6 | guests made before slice 3 need one root command so that their agent can update itself (ARCH §5) | STATUS | C6: README "upgrade" section |
| O7 | lows on record that were dropped with a reason (D-415, D-433) and the follow-ups of slice 8 reviews (for example #366: the note on hand-made VM disks; #367: no hard cap when the issue part alone passes 64 KiB) | ROADMAP rows 33, 40; PR reviews | C9: one sweep of the slice 8 review comments; each low either a small card or "dropped" with a reason in `docs/decisions/` |
| O8 | the user's own installation still has the rights of slice 6 (U7, U8 open since slice 7) | STATUS | U7, U8 |

## 3. What a release needs
| Need | State | Card |
|---|---|---|
| **Version** | `main.version` by ldflags, `plumb --version`, `plumb-agent version`, `info.version: 0.1.0` in `openapi.yaml`. Proposal: **v0.1.0**, a pre-1.0 version (the API may still change; the README says so) | C1 |
| **Changelog** | none. Proposal: `CHANGELOG.md` with one entry "v0.1.0" (the features per BRIEF section, the known limits, the rights the token needs), written from STATUS and the slice plans | C6 |
| **Build** | `package-release.sh` (reproducible flags `-trimpath -buildvcs=false -buildid=`), `SHA256SUMS` inside and next to the tarball; the workflow makes a **draft** only and refuses an existing tag | – (exists) |
| **Sign** | not signed. Proposal: a GitHub build provenance attestation in the workflow (`actions/attest-build-provenance`, OIDC: no key and no repo secret), plus the `SHA256SUMS`. A key of our own (minisign, GPG) would need a repo secret: a repo setting, so a USER point | C4, U6 |
| **Installer checks the artifacts** | the installer checks its copies into the LXC, but not the tarball's `SHA256SUMS` before it uses `plumb` and `plumb-agent` (D-421) | C3 |
| **Install test** | a clean install from the tarball on `pve-dev` (after `--remove --keep-guests` of the test controller, or a second LXC id) | C7 |
| **Upgrade test** | there is no earlier tag. The upgrade paths are: (a) the test controller on the last `main` → the RC tarball (the user's own install will take this path); (b) RC1 → RC2 tarball (the path of every later release). RCs are built **locally** with `package-release.sh`, never with the workflow, and never pushed as a tag | C7 |
| **Docs check** | README: install from the tarball instead of the two `make build` lines (user decision (a), "until there is a release"), upgrade, the rights table, the limits (U9, U10, O5, O6), "Explain an Issue" (C10 of slice 8) | C6 |
| **Leak scan** | the git history (all refs on `origin`, also merged branches), the tarball, the web bundle (source maps are off, `vite.config` `sourcemap: false`: check the bundle anyway), the release text, and on `pve-dev` after C7: DB, logs, events, export, Plumb backups, guest backups (D-421) | C5 |
| **Full host re-run** | every slice's acceptance list once more on the RC, incl. R1 (a fresh Plumb restored from a backup reconciles every Target), the `vmbr9` NIC proof, a guest with a data disk | C7 |

## 4. USER questions (ask in one message when C7 has passed; D-326 (4))
| # | Question | Team recommendation |
|---|---|---|
| U6 | **Release steps.** (1) the version **v0.1.0**; (2) a GitHub build provenance attestation in the workflow (no secret); (3) pl-lead runs the release workflow, which makes a **draft**; (4) **you** read the draft and publish it (publishing creates the tag); (5) after it the README install path points to the release | yes to all five; publishing stays yours |
| U7 | **A repair run of your own installation** for the rights of slice 7 (`VM.Backup` on the pool, `Datastore.AllocateSpace` + `Datastore.Audit` on your guest backup storage, `SDN.Use` per extra bridge / tag). Slice 8 adds no right (D-423). You run `install.sh --dry-run` first; we send you the exact command and the expected ACL lines | yes, at the release, with the release tarball; **you** run it (only `pve-dev` is ours) |
| U8 | **Your networks and backup storage** for that run: which extra bridges and VLAN tags (`--extra-bridge`, `--extra-vlan`); which storage holds guest backups; and, if it is not a PBS with client-side encryption, do you accept guest backups in clear there (`--guest-backup-clear`, D-406)? A clear backup holds the agent credential and the desired list of each guest | name only the bridges and tags that your Targets use; a PBS with a client key; "clear" only if you say so |
| U9 | **PBS** has never run against a real Proxmox Backup Server (`pve-dev` has none). (a) you name a PBS and a datastore for one test run, and say whether it may hold test backups; or (b) the README says "PBS: the encryption state is read from the storage config; not tested on a real PBS" | **(b)**, unless you have a PBS at hand (D-434) |
| U10 | **Restore of a guest that is gone** needs `Datastore.Allocate` on the guest backup storage, a right that opens every volume there. Yes or no? | **no** (D-435): the README shows the way by hand (restore in Proxmox into pool `plumb`, then adopt) |

## 5. Cards (draft; numbers from pl-lead)
| # | Title | Owner | Reviewer | Depends on | Notes |
|---|---|---|---|---|---|
| C1 | Release contract: version v0.1.0 (ldflags, `openapi.yaml` `info.version`, `plumb --version`), the release checklist (this file → `docs/RELEASE.md`), the changelog format | pl-architect | pl-reviewer | slice 8 done | small; decides what C2 … C9 check |
| C2 | Leftovers: `npm audit`, `govulncheck` (GO-2026-5932), module updates that a fix needs | pl-codex-fix | pl-architect | C1 | record what stays in the changelog |
| C3 | Installer: check `SHA256SUMS` of the unpacked tarball before it uses a binary (refuse a mismatch or a missing file, name the file); the upgrade path from a tarball over an existing install (keeps data, keys, ACLs; dry-run shows the version change) | pl-backend | **pl-reviewer** (installer = critical) | C1 | tests with a damaged tarball, a missing `SHA256SUMS`, a swapped binary |
| C4 | Release workflow: build provenance attestation (only if U6 says yes; until then a branch), a check that `web/dist` has no `.map` file and no test key (D-369), the release notes template with the known limits | pl-codex-qa | pl-reviewer | C1 | no run of the workflow in this card |
| C5 | Leak hunt: git history of all refs on `origin` (a secret scanner over every commit), the RC tarball, the web bundle, the release text; after C7 the host data (DB, logs, events, export, Plumb and guest backups) | pl-reviewer | pl-architect | C3, C4 (RC), C7 (host part) | zero hits or a fix card; a secret in history → stop and ask the user (history rewrite = force-push, forbidden for agents) |
| C6 | README and `CHANGELOG.md`: install from the release tarball, upgrade (incl. O6), the rights table, limits (U9 text, U10 way by hand, O5), the slice 8 features | pl-frontend | pl-architect | C1, U9/U10 answers | pictures from the mock only; the install path switches when U6 is yes |
| C7 | Full host re-run on `pve-dev` with an RC tarball (built locally): clean install, upgrade (a) and (b), every slice's acceptance list, R1 restore-from-backup reconciles every Target, the `vmbr9` NIC proof, a guest with a data disk, the damaged-input restores | pl-backend (host) | pl-reviewer | C2, C3, C4 | announce controller-down steps; host clean at the end except `vmbr9` |
| C8 | `vmbr9` undo on `pve-dev` (D-432): remove the bridge with the files on the host, the repair run without `--extra-bridge vmbr9`, the ACL view shows 9 lines again | pl-backend (host) | pl-reviewer | C7 | pl-lead writes it; the only network change allowed (D-379) |
| C9 | QA sweep: 20 runs of the VM guest job for O1, the slice 8 lows (O7), mutants of C3 | pl-codex-qa | pl-architect | C3 | O1 found → fix card before C10 |
| C10 | Release (after the user's U6 yes): pl-lead runs the workflow for v0.1.0, checks the draft against `SHA256SUMS` and the attestation, **the user publishes**; then the user's own repair run U7/U8 with that tarball (the user runs it) | pl-lead, the user | – | C5 … C9, U6, U7, U8 | the only public step |

**Order:** C1 → C2, C3, C4, C6 in parallel → C9 (after C3) → C7 → C5 (host part) and C8 → the USER message (U6 … U10) → C10. U9 and U10 can be asked earlier, because their answers only change README text (C6).

## 6. Risks
- **A secret in the git history** (C5): a fix needs a history rewrite, which is a force-push (forbidden for agents) and a public step: stop and ask the user.
- **The first upgrade path is untested by the user's data:** the user's install runs an old controller (slice 6 rights). C7 (a) runs that path on `pve-dev` from the last `main`; the user's own run (U7) uses `--dry-run` first.
- **No earlier tag:** the attestation and `SHA256SUMS` prove the tarball, not the code before it. The changelog says that v0.1.0 is the first release.
- **`vmbr9` undo after the last NIC proof:** after C8 a NIC regression can only be proven by a new D-379 card.
