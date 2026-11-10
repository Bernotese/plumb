# Status

Product: **Plumb** – self-hosted infrastructure controller for a Proxmox homelab. Brief: `docs/BRIEF.md`.
(Naming: the product is "Plumb". The herdr workspace and local folder are still called `plump`.)

## Phase
Architecture. **No feature code** until `docs/ARCHITECTURE.md` is reviewed by `pl-reviewer` and approved by the user.

## Tasks
| Card | Owner | State |
|---|---|---|
| T-001 architecture + first task cards | pl-architect | done, PR #1 open |
| T-002 architecture review (incl. security) | pl-reviewer | done: changes needed (4 must) |
| T-016 architecture rework | pl-architect | done (937479e), second review by pl-reviewer running |
| T-019 record user answers | pl-architect | in progress |

## Done
- 2026-11-10 · Team setup, auto mode, GitHub remote, model split.
- 2026-11-10 · Brief stored, decisions D-001 … D-017 seeded.

## Open for the user
- Provide the Proxmox test host (PVE ≥ 9.1; needs: ARCHITECTURE §8). Planned from 2026-11-11. Blocks T-012 and T-013.
- Questions 2–5 answered on 2026-11-10 (see T-019).
- Approve the architecture after the rework (T-016) is re-reviewed.
