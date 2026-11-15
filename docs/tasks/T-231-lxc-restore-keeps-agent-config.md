# T-231 – LXC backup: exclude only the agent's secrets, so a restored container can re-enroll (host finding)

- **Owner:** pl-architect (author of T-221)   · **Reviewer:** pl-reviewer (backup secrets = critical)   · **QA:** –
- **Size:** S   · **Depends on:** –

## Goal
Found on the real host (T-225 part 1): a restore over a Plumb-made LXC leaves the Target without a way back. The LXC backup excludes `/etc/plumb-agent` **and** `/var/lib/plumb-agent` (L1 of #321). So the restored container has no `/etc/plumb-agent/agent.json` (link URL + CA); the re-enroll Plan sets the token, but the agent fails at every start (`open /etc/plumb-agent/agent.json: no such file`, 90 restarts), the re-enroll Run fails after 10 min, Issue `agent:silent`. The restore itself is right (old credential 401, container stays unprivileged). The VM path (no excludes) works.

## Acceptance criteria
- [ ] List first, in the PR, every file the agent keeps under `/etc/plumb-agent` and `/var/lib/plumb-agent` (config, CA, firewall and network boot files, safe-apply copies, the credential, a one-shot `enroll.token`, the state) and mark each: secret yes/no, needed after a restore yes/no.
- [ ] The backup excludes exactly the secrets (the credential; a left-over `enroll.token`), by path, and keeps what the agent needs to start and re-enroll. A VM backup cannot exclude paths: say in ARCH §20 and for the README what a VM backup holds (the credential is revoked at a restore, D-410; an old backup's credential is dead after any re-enroll).
- [ ] Fail safe for backups already made with the old exclude list (the host has one): the re-enroll after a restore must work for them too – the re-enroll Plan of an LXC writes the config (agent.json, CA) again through the driver (as the first enrollment does) instead of relying on the restored files. Prefer this as the main fix if it is small: then the exclude list can stay wide. Decide and say why.
- [ ] Tests: a restored LXC without `/etc/plumb-agent` re-enrolls (fake driver + the CI guest job if it can show it); the credential is in no backup path list.
- [ ] `make lint test`; PR to `main`; report. Proven on the host in T-225 (the kept test CT `t225-ct`).

## Result (owner, max 8 lines)
branch · commit · tests · notes
