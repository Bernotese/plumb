# T-072 Part 2 — PR #117 web QA

Audited head `9611f60`, including review fixes `45659ab` and merge `e226efa`.
S1 is present: TargetForm requests `{target, status: "open"}`; HTTP test checks the exact query.

| Web criterion | Code and tests | Result |
| --- | --- | --- |
| Configured address plus reported list and guest mark | Targets.svelte, TargetForm.svelte; existing list/mismatch tests; new Target-page configured/list test | PASS |
| Hide reported values when reported_at is null | reportedAddrs; new parameterized list/detail tests inject a stale nonempty list and more flag | PASS |
| More marker | reportedText; existing list/unit tests; new Target-page more test | PASS |
| Mismatch line and Issue link | TargetForm.svelte; existing mismatch text and href assertions | PASS |
| guest:address Issue text, guest source, restart hint, ignore-only actions | IssueView.svelte; existing two-action, no accept/revert, hint and ignore-once tests | PASS |
| Render addresses as text | Svelte interpolation; existing list injection test; new Target/mismatch/Issue injection tests assert literal text and no img/onerror element | PASS |

Validation: `cd web && npm run lint && npm test` PASS; 20 files, 279 tests.
Added five missing screen cases in `addrs-qa.test.ts`. No product changes.
Browser-shot: list, Target db1, Issue 10, each light and dark; all HTTP 200, zero console errors.
Viewed all six images: labels and addresses readable, mismatch link visible, Issue hint and ignore controls visible; no clipping found.
Local artifacts: `/tmp/t072-web-{list,target,issue}-{light,dark}.png`.
The mock has a mismatch demo. Playwright automation was not run; browser-shot performed the browser review requested for this pass.
No Proxmox host or SSH used.
