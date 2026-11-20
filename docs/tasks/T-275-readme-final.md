# T-275 – README: the final state (cleanup point 6)

- **Owner:** pl-backend (after T-274)   · **Reviewer:** pl-reviewer; `pl-lead` looks at every picture   · **QA:** –
- **Size:** M   · **Depends on:** T-270 (what is really built), T-274 (the terms)

## Goal
User order D-442, point 6. The README shows the product as it is at the first release, for a user, and is shorter (819 lines now).

## Acceptance criteria
- [ ] The features of slices 1 to 8 as they are on `main` (the table of T-270 is the source); install (still the `make build` lines until the release; the release text is ready in #398 / #405), upgrade, the rights table (its test stays green), restore, the known limits.
- [ ] User docs only: no developer notes, no decision or task numbers, no host data, no release text.
- [ ] Shorter: remove repeats, move long reference parts that few users need into `docs/` pages that the README links (name them); say the line count before and after.
- [ ] Screenshots: each picture in the README shows today's UI. Re-take from the mock (`serve --dev --dev-demo`, `?as=admin`) what changed; unused pictures go (T-272 has the list). `browser-shot` for each; `pl-lead` looks at each new picture before the merge.
- [ ] Every sentence true against the code (the evidence comment maps the changed sentences to code or a host log). `make lint`; the README tests green; PR; report.

## Result (owner, max 8 lines)
branch · commit · tests · notes
