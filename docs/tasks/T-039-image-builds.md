# T-039 – Image builds: Blocks, seal, retention, schedule   (test host ready since 2026-11-11; needs the plan)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (will be split)   · **Depends on:** test host (D-041), T-012 and the agent PRs (#8, #13) on `main`

## Goal
Plumb builds its own templates from a base image plus Blocks, keeps versions, removes old ones and rebuilds on a schedule.

## Design (decision level – D-098, BRIEF §5.4, ARCHITECTURE §12)
- `Image` spec gets `blocks`, `retention.keep`, `schedule`.
- Build = Run: temporary guest → apply Blocks → agent `seal` → stop → template → image version row.
- A build never changes a target. A target records its image version.
- Blocks of an Image cannot use secret references. `seal` also removes the agent's desired list, so a template holds no secret.
- Retention keeps the newest `keep` versions and every version that a guest still needs.
- A scheduled build is planned by `system`. Its approval rule waits for the user's answer (T-025, question 2).

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: template conversion, linked and full clones per storage, what `seal` must reset on Debian 13 and Ubuntu 26.04, delete rules for templates with clones.
- [ ] Parts that need no host (schema, version table, retention choice, schedule clock) can become their own unblocked card then.
- [ ] Needs new architecture decisions.
- [ ] Facts from the real host that the plan must cover (pl-lead, 2026-11-11):
  - D-192: `seal` (or a Block) removes initramfs networking on dracut images (Ubuntu 26.04), so a Plumb-built Ubuntu template takes a static address. Proof on the host: static address applied at the first and at a later boot.
  - D-185 / pl-architect: an image build must remove the SSH host keys before the template is made.
  - D-188 (T-055): there is no Image binding; a template is found by name + full SHA-256 in the pool. Templates and import files are never deleted by the driver (D-077): the retention rule of this card decides who deletes them.

## Out of scope
- Signed image checksums. Images for other distributions.

## Result (owner, max 8 lines)
branch · commit · tests · notes
