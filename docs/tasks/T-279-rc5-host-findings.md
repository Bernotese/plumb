# T-279 – Small fixes from the RC5 host run (T-277)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** the CI jobs; RC6 on the host (short)
- **Size:** S–M   · **Depends on:** T-277 steps done

## Goal
What the first look at the web UI on a real controller showed (T-277 c1) and two text points. One PR. After it: RC6 and a short host check.

## Acceptance criteria
- [ ] U1: no decision or task number in a schema `description` or `title` (`internal/state/schema/*.json`, `internal/resource/schema/*.json`: the UI shows them as field hints, e.g. "Known limit (D-192, D-220, D-223)"). Each sentence still says what the user needs. The `usertext` guard reads these JSON texts too (a mutant fails it); the web guard too if the web has its own copy.
- [ ] U2: the Targets table shows "Last seen" at 1280 px when a guest reports an IPv6 address next to its IPv4 one: the address cell wraps or shows the first address + "N more" (the full list stays on the Target page); nothing is cut off at 390 px either (which columns show there: say it). A test with a long address list; `browser-shot` pictures of the mock at 1280 and 390 px with such data in the PR's evidence (mock data only).
- [ ] U4: Settings → General, "Left behind": the text says how a line for a guest that is gone goes away (make a new repair key, then revoke the previous one).
- [ ] U5: the refusal for a `symlink` whose target is an agent path names the attribute `target` and the resolved path (today it says "path resolves to …").
- [ ] Notes for `docs/plans/after-release-slice.md` (no code): U3 the Plan page names a token by its id (show its name); N1 events are stream-only.
- [ ] No API field, key, code or stored value changes. `make lint test`; the e2e job green; evidence in the PR first; PR; report.

## After the merge: RC6 and a short host check (same hard rules as T-277)
- [ ] RC6 built locally from main's head (commit, 4 hashes, the 4 expected release hashes R6-0, the repro check vs the CI build); the bundle check; the RC5 → RC6 upgrade (dry run → `pl-lead` → go; 0 `pveum` lines).
- [ ] The findings seen fixed on the real controller: the Target form's hints without numbers and the Targets table with a dual-stack guest at 1280 and 390 px (pictures through the read-only proxy, never committed), the 'Left behind' text.
- [ ] The built-container path, which no host run has proven (review note of #424): one container Image BUILT with a Block → a Target from it → it enrolls once, and again after a reboot.
- [ ] C5b of the RC6 tarball (pl-reviewer), cleanup with `pl-lead`'s go, end proof; the log as `docs/tasks/T-279.log.md`.

## Result (owner, max 8 lines)
branch · commit · tests · notes
