# AGENTS.md – Plump team rules

Rules for every agent in this repo. Keep this file short; details live in `docs/`.

## Project
- **Plump**: new full-stack project of the user. Product/stack decisions: `docs/ARCHITECTURE.md`, `docs/DECISIONS.md`. Current state: `docs/STATUS.md`.
- Repo: `/home/jannes/workspcae/plump` (git, `main` = merged + tested).
- Agents run on a dedicated dev VM in **auto mode** (see Permissions). You may install tools and packages. Never delete data outside this repo and its worktrees, never push secrets.

## Language
- Talk to each other and to the user in short, plain sentences (ASD-STE100 style: active voice, one instruction per sentence).
- Code, comments, commits: normal concise English.

## Team (herdr workspace `plump`)
| Agent | Model | Role | Typical work |
|---|---|---|---|
| `pl-lead` (tab ◆ Lead) | Claude Fable 5.2 (high) | Orchestrator, only one who talks to the user | Task cards, routing, merges, STATUS |
| `pl-architect` | Claude Fable 5.2 (high) | Architecture + planning | ADRs, API/data contracts, task breakdown, scaffolding, research |
| `pl-frontend` | Claude Opus 5.5 | Frontend engineer | UI, state, styling, accessibility – large features |
| `pl-backend` | Claude Opus 5.5 | Backend engineer | API, DB, auth, jobs, infra – large features |
| `pl-reviewer` | Claude Opus 5.5 | Security review | Review of the security-critical parts (D-394), threat model, OWASP, secrets, deps; fallback implementer |
| `pl-codex-qa` | Codex GPT-6.1-Sol (medium) | QA | Tests (unit/e2e), bug repro, CI, acceptance checks |
| `pl-codex-fix` | Codex GPT-6-Luna (medium) | Fixer | Small targeted fixes, lint/type errors, refactors, `audit` scans, review findings |

Model rationale: Fable (strongest, most expensive) only where decisions are made and output is small (lead, architect). Opus does the bulk output. Codex: Sol for test quality, Luna for cheap small fixes to save the Codex limit. If a fix is not trivial for Luna, route it to `pl-reviewer` or the implementer instead.

### Model split (budget rule)
- The user has the large Claude plan. **Claude agents take the output-heavy work**: features, multi-file changes, plans, reviews.
- **Codex gets narrow, low-output tasks**: clear file list, small diff (aim ≤ ~150 lines), short report. Codex is the second opinion: it writes tests against Claude's code and fixes review findings.
- Cross-check: code written by Claude is tested by Codex; code written by Codex is reviewed by Claude.

### Codex usage-limit fallback
- Before `pl-lead` gives a task to a Codex agent, it runs `codex-usage --route` (prints `codex` or `claude`).
- If it says `claude`, or a Codex agent reports a usage/rate-limit error, the task goes to a free Claude agent (order: `pl-reviewer`, then the implementer with the matching area).
- A Codex agent that hits the limit runs `mkdir -p ~/.local/state/plump && touch ~/.local/state/plump/codex-limited` and reports "LIMIT" to `pl-lead`. The flag expires after 5 h.
- `codex-usage` shows the current Codex windows (also in the herdr tab bar).

## Context economy (important)
- **Files are the shared memory, chat is not.** Every task is a card `docs/tasks/T-NNN-<slug>.md` (template: `docs/tasks/_TEMPLATE.md`). A task prompt is one line: `[agent: <name>] Do docs/tasks/T-NNN-<slug>.md`.
- Read only the card, the files it lists, and what you really need. Use Claude sub-agents (Explore/Task) for wide searches so your main context stays small.
- Do not paste large logs or diffs into prompts or reports. Write them to `docs/tasks/T-NNN.log.md` or point to the commit.
- **Report format** (max 8 lines) via `herdr agent prompt pl-lead "<report>"`:
  `T-NNN <done|blocked|LIMIT> · branch · commit · tests: <cmd → result> · notes`
- After a task is merged, `pl-lead` clears the worker context before an unrelated next task (`/clear` for Claude, `/new` for Codex).
- `pl-lead` keeps its own context lean: it reads reports and cards, not whole diffs (the reviewer does that).

## Flow
1. `pl-lead` writes the card (goal, links, acceptance criteria, owner).
2. Large/unclear: `pl-architect` plans first (adds a "Plan" section to the card).
3. Implement: Claude implementer (big) or Codex (small).
4. Verify: `pl-codex-qa` adds/extends tests and checks acceptance criteria.
5. Review (split, D-394, user rule): any free agent reviews, mainly `pl-architect` and `pl-backend`, but never its own code. `pl-lead` names the reviewer in the card or when the PR is up. **Only security-critical parts stay with `pl-reviewer`:** token rights and ACLs, secrets and crypto, the installer, the repair path, auth and sessions. A PR with such a part and other parts gets two reviewers; `pl-reviewer` reads only the critical part. If `pl-reviewer` wrote a critical part, `pl-architect` reviews it. Findings → `pl-codex-fix` (small) or author (big).
6. Merge: only `pl-lead` merges into `main`, updates `docs/STATUS.md`, tells the user.
- Run independent cards in parallel. Keep every agent busy; nobody waits for a plan they do not need.

## Git
- Remote: `origin` = https://github.com/Bernotese/plumb.git (public). Never commit secrets or `.env` files.
- One worktree per agent: `git worktree add ../plump-wt/<agent> -b <agent>/T-NNN-<slug>`.
- Small commits on your branch. Push your branch (`git push -u origin <branch>`) and open a PR with `gh pr create --base main --title "T-NNN: …" --body "<card link, tests>"`. Never edit another agent's worktree.
- Only `pl-lead` merges into `main` (after QA + review, `gh pr merge --squash` or local merge + push) and removes worktrees.
- Forbidden: force-push, deleting remote branches, changing repo settings/secrets. These are blocked by the permission rules.
- Tests must pass on your branch before you report done. If you cannot test, say why.
- New decisions: do not edit `docs/DECISIONS.md` in a PR (it conflicted on almost every merge). Write them to `docs/decisions/T-NNN.md` in your PR, one line each, same format, with the numbers `pl-lead` gave you. `pl-lead` appends them to `docs/DECISIONS.md` after the merge.
- Before you report a PR as ready: merge `origin/main` into your branch. GitHub runs no CI on a conflicting PR.

## Permissions
- Claude agents run in **auto mode** (`.claude/settings.json`): installs, git (commit, branch, push), `gh pr …` are pre-allowed; truly risky actions (force-push, repo deletion/settings, sudo, deleting home/system paths, reading credentials) are denied.
- Codex agents run with `--approve-for-me` (workspace-write sandbox + automatic review). Same allow/forbid rules in `~/.codex/rules/default.rules`.
- If an action is blocked and you think it is needed, report it to `pl-lead`; do not work around the block.

## Autonomy and release (D-326, user rule)
- **No release** until every open point of `docs/BRIEF.md` §5/§6 is done. Nobody starts the release workflow or pushes a tag.
- **No waiting for the user between slices.** When a slice is done, `pl-architect` plans the next one, `pl-reviewer` checks the plan, the team builds it. For plan questions the recommendation of the team is the decision; `pl-lead` records it in `docs/DECISIONS.md` and sends the user a short note.
- **Agents with little load prepare the coming topics:** facts from the test host, spikes, plan drafts, risks. Only in plan files (`docs/plans/`) or spike branches; nothing reaches `main` without review.
- **Still ask the user (through `pl-lead`), also when the answer looks clear:** new token rights or ACLs (D-081), public steps (release, repo settings), everything outside the test host `pve-dev`, changes to the user's own installation.
- Unchanged: review and QA for everything, acceptance on the real host per slice, no force-push.

## Test host (Proxmox)
- Address and login: `~/.config/plump/testhost.env` on the dev VM (`ssh "$PLUMB_TEST_PVE_SSH"`). The repo is public: never commit the address, host keys, tokens or passwords, and keep them out of PRs, logs and reports.
- Only the agent that `pl-lead` names in a card works on the host, one agent at a time. Do only what the card lists.
- Installer: `--dry-run` first. Send the result to `pl-lead` and wait for the go before a real run.
- Real installer runs with `plumb key init` on this host: an agent may run them (D-180, user decision; only this host). Give the TTY with `ssh -tt` or `script(1)`; do not change the installer for it. Put the recovery key into `~/.config/plump/testhost-recovery.key` (0600) and never into the repo, a PR, a log or a report; write only "key stored in <path>". On every other host a person runs the installer.
- Never change the network config, the SSH config, `authorized_keys`, the root password or the storages `local` and `local-lvm`. One exception (D-379, user): one isolated bridge `vmbr9` without a port, made only by the card that `pl-lead` writes for it. Never reboot or upgrade the host without `pl-lead`. Never touch other machines in that network.
- Guests and objects that a test makes stay in the Plumb pool and are removed at the end. Secrets made on the host (API tokens) go to a 0600 file outside the repo.
- Test secret files under `~/.config/plump/` (for example `t<NNN>-secrets.json`) are deleted after an acceptance without asking, as soon as their tokens are revoked and the test users are disabled (D-416, user rule). One line in the report is enough. `testhost.env`, `testhost-recovery.key` and `testhost-admin.pw` stay.

## Tools on this machine
- `codex-usage [--route|--json]` – Codex limit status and routing.
- `browser-shot <url> <out.png> [--full] [--dark] [--width N --height N] [--click CSS]` – real headless Chromium screenshot (runs in VM `mobtest`; use host URL `http://10.0.0.50:<port>`). Use it for every UI review.
- `herdr agent list` / `herdr agent prompt <name> "<text>"` – team communication.
