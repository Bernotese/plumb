# Risks of the coming topics (T-140, draft)

Author: pl-reviewer. Input for `pl-architect` (T-138). No decisions here: each "Rule" is what the plan must hold or replace with a better answer.
Base: D-326, BRIEF §5.9, ARCHITECTURE §7, and the pattern of the slice 4 findings:
- **Guest data treated as trusted** (#218 M1: Accept weakened the firewall; D-323 made every Accept human-only).
- **Old credentials and identities live again after a state change** (#222/#224: backup password works again, D-311; reused VMID = wrong guest, D-320/D-322).
- **Secrets leak through side paths** (#236 S1: schema error text echoed the value; #206 H1: vzdump holds the secrets in clear).
- **A token reaches a power that only a person should have**, through a second path (D-323 known limit).
Every plan names its new D-081 rights. A new right is a user question (D-326 (4)).

## 1. User management (roles, second admin, password reset, lockout, last admin, sessions)
- **Assets:** admin sessions (approve `destructive`, manage tokens, secrets, import); passwords; the provenance `user:<name>`.
- **Attacker:** a user with a lower role; a stolen session cookie; an API token (any tier); an online password guesser; a person who knew an old password.
- **Worst cases:**
  1. A lower role raises itself or a peer (edits its own role, creates a token with more than its own rights, resets an admin password).
  2. A token manages users or sessions (a second path, like the D-323 limit).
  3. The last admin is deleted, demoted or locked (two parallel requests each see "one other admin left").
  4. A role change, password reset or delete does not end the open sessions and tokens of that user; a new user with an old name inherits tokens, sessions or ignore rules.
  5. Lockout as a weapon: a guesser locks the only admin out for good.
  6. User names leak through other answers or timing (404 vs 401, argon2id only for known names).
- **Rule:**
  - User, role and session management is human-session-only; no tier of a token reaches it (like tokens and secrets today).
  - Role check on the server per operation, from one table; a user never changes its own role.
  - A token never has more than its owner; a role loss, lock or delete disables the tokens of that user at once (or tokens get no owner rights at all: architect decides).
  - Last-admin rule in one DB transaction (count + change), also for delete, demote, lock and restore (D-311).
  - Every password or role change ends all sessions of that user except the current one; a delete ends all. Users get a stable id; provenance and tokens bind to the id, not the name.
  - Lockout is per (name, source) with backoff and an end time, never a permanent lock; the rate limit stays.
  - Password reset stays D-321 (server-made one-time password, no-store, hash only); a reset of an admin needs an admin.
- **Tests:** API matrix (role × endpoint × token tier) from the OpenAPI file, so a new endpoint with no rule fails the test; parallel demote of the two last admins (one must get 409); session cookie after password change / role change / delete → 401; token of a demoted user → 401 or reduced; recreated name sees no old token; 100 bad logins → admin still logs in from another source; same status and similar time for unknown and known names.

## 2. Adoption of existing guests (D-323)
- **Assets:** the data of a guest that Plumb did not build; the user's guests outside the pool; the desired state that adoption writes.
- **Attacker:** root in the guest (it may be compromised before we see it); a token client that names a VMID; a person who renames or recreates a guest between Plan and Run.
- **Worst cases:**
  1. Adoption writes guest-reported values into the desired state (keys, apt sources, units) and a token approves it.
  2. Wrong guest: the VMID changed owner between observe and apply (VMID reuse, D-320); a later Target delete destroys the user's data.
  3. Adoption moves a guest into the pool: it needs `Pool.Allocate` or rights on VMs outside the pool (new D-081 rights); a token can then reach any guest of the host.
  4. The Proxmox config of the guest holds secrets (`cipassword`, `sshkeys`, LXC `env`, description); they land in documents, Plans, events or logs in clear.
  5. The first agent install needs a way in (SSH or guest agent); see topic 3. An adopted guest is not sealed: old host keys, old machine-id, foreign users with root.
  6. Large or hostile reports (many files, deep JSON, control chars) fill the DB or break the UI.
- **Rule:**
  - Every adopt Plan is human-only (reason like `accept-guest-data`), set from the origin by the server.
  - The Plan names the guest by node, VMID **and** name and a config digest; the Run checks all three before each write and refuses on a mismatch.
  - An adopted guest is never deleted by Plumb by default: delete of its Target only unbinds; a disk delete is a separate `destructive` human choice.
  - Imported Proxmox fields go through `wire.Redact` and the secret rules; no adoption of `cipassword`/`sshkeys` values, only "present".
  - Only guests the user put into the pool by hand, or a user question for any new right. No adoption of LXC with `privileged` or host bind mounts without a red warning.
  - Report size limits and schema check as for agent reports.
- **Tests:** token of tier `apply` approves an adopt Plan → 403 `accept-guest-data`; swap the guest on the VMID between Plan and Run (fake driver) → Run refuses; Target delete of an adopted guest → no `DELETE` call to Proxmox; VM config with `cipassword` → value in no table, event or log (grep the DB file); oversized report → 413 and nothing stored.

## 3. SSH repair fallback (key storage, host key trust, what it may run)
- **Assets:** the repair private key (root on every guest that trusts it); the guests; the controller's trust in its own agents.
- **Attacker:** root in one guest; a host on the same bridge (ARP/DNS spoof); a reader of a backup or export; a token client.
- **Worst cases:**
  1. The private key leaks (backup, export, log, `/proc`, a tmp file) and gives root on all guests.
  2. Plumb trusts the first host key it sees (TOFU) on a broken guest: a spoofer on the bridge gets the repair session and the new agent credential.
  3. Repair runs free commands, so it is a second `command` resource with no Plan or with a token approval.
  4. A compromised guest uses the repair session: agent forwarding, port forwarding, or hostile output in the UI and log; it reads the new enrollment token.
  5. The public key in `authorized_keys` is a standing backdoor: no `from=`, no forced command, never rotated, not removed on unbind.
  6. A guest agent path instead of SSH needs `VM.GuestAgent.Unrestricted`: root on every pool VM through the token (new D-081 right).
- **Rule:**
  - The key is a secret of the secret store (master key, `backup=0` rule); never in an export without passphrase, never in a log; in memory only for the session.
  - Host keys are pinned while the agent is healthy (the agent reports the fingerprint over the pinned TLS link); repair refuses an unknown or changed host key. No TOFU, or TOFU only as a human choice that shows the fingerprint.
  - Repair does one fixed action set (put the agent binary, write a new enrollment token, restart the agent); it is a Plan, risk `disruptive` or higher, human-only.
  - `authorized_keys` line: `restrict,from="<controller IP>",command="<fixed repair command>"`; no forwarding; key rotation and removal on unbind are part of the plan.
  - Guest output is untrusted: size limit, escaped, never parsed as a command.
  - LXC: the controller has no host root, so `pct exec` is out; say what repair means for LXC.
- **Tests:** backup and export (no passphrase) contain no key bytes (grep); changed host key on a fake SSH server → repair refuses, no command sent; repair Plan approved by `apply` token → 403; the `authorized_keys` line in a test guest has `restrict`, `from=`, `command=`; a forwarding request is refused; unbind removes the line.

## 4. MCP / AI help (token tiers, prompt injection through guest data, human-only rules)
- **Assets:** the API token of the MCP layer; the human approver's attention; the guests (an `apply` token is root-equivalent, ARCHITECTURE §7).
- **Attacker:** root in a guest that writes text into a file, unit, hostname, package description or issue diff; a web page or doc the model reads; the model itself making mistakes in a loop.
- **Worst cases:**
  1. Prompt injection in guest data makes the model propose a weakening change (firewall, SSH key, apt source) and an `apply` token approves it.
  2. The model copies observed values of an Issue into a normal proposal (the D-323 known limit), so Accept-human-only is bypassed.
  3. AI text (comment, Plan reason, "summary") tells the human "safe, approve"; the approval page shows the AI text more clearly than the real diff.
  4. Human-only rules live only in the MCP layer, so a direct API call skips them.
  5. An HTTP MCP endpoint without auth or origin check (DNS rebinding from the user's browser) gives the token to any web page.
  6. A loop makes thousands of Plans, comments or events (DB growth, a flood in the inbox).
- **Rule:**
  - MCP is a pure API client with one token; no session, no own rights, no new endpoint. Every human-only rule stays on the server.
  - Recommended default tier for AI: `plan`. The docs say: `apply` for an AI = root on the guests.
  - Guest-sourced text in MCP answers is marked as data (a field, not prose); the server never puts guest text into a Plan reason.
  - The approval page shows the real changes first; AI text is labelled with its token and shown below; never rendered as HTML.
  - Plans whose `to` values equal observed values of an open Issue on the same Target (Accept by copy) get a warning or the `accept-guest-data` reason: architect decides, the plan must answer the known limit.
  - MCP over stdio by default; an HTTP mode needs a token header and an origin/host check.
  - Per-token rate limit on proposals and comments.
- **Tests:** a guest file holding an injection text appears in the MCP answer only inside a data field; `plan` token proposal that copies an Issue's observed SSH key → warning/reason present; `apply` token approve of such a Plan → refused (if the rule is taken); comment with `<script>` → escaped in UI (browser-shot); HTTP MCP without token or with foreign `Host` → 401/403; 1000 proposals in a minute → 429.

## 5. Signed image checksums (key pinning, key rotation)
- **Assets:** every image and so every new guest; the trust root (Debian/Ubuntu/Proxmox signing keys).
- **Attacker:** a mirror or network path (images may come over http, LXC templates are http only); a person who can change Plumb settings or a token that edits an Image document.
- **Worst cases:**
  1. The key comes over the same channel as the checksum file (TOFU from the mirror), so a mirror signs its own list.
  2. A token or document field sets the trusted key, so `plan` + `apply` tokens pick any image.
  3. Replay or downgrade: an old, validly signed checksum list of a vulnerable image.
  4. Clearsign parsing: text outside the signed part, or a second file name with the same name pattern, is read as trusted.
  5. Key rotation: a vendor rotates and every build fails, or the team adds a new key by a weak path (a URL fetch at run time).
  6. A large OpenPGP parser as a new dependency (deprecated `x/crypto/openpgp`), parsing untrusted input.
- **Rule:**
  - Keys are pinned in the binary (fingerprint + key), per vendor and release; no API field adds a key. A new key comes with a reviewed Plumb release or a human-only settings change that shows the fingerprint.
  - Only the signed bytes are parsed; exact file-name match; one entry per name, else refuse.
  - Refuse a list older than the one used before for this Image (date in the signed data), unless a person approves a downgrade.
  - The pin of D-224 stays valid as the human path; the signed path does not weaken it.
  - One small, maintained parser; fuzz it.
- **Tests:** list signed by an unpinned key → refused; valid signature but changed byte outside the signed part → ignored; duplicate file name → refused; older signed list → refused; token proposal that sets a key → 400/403; fuzz target for the parser in CI.

## 6. Guest backups (secrets in vzdump, #206 H1)
- **Assets:** guest data; in the guest: the agent credential, the agent's desired list with secret values (0600), SSH host keys, app secrets; the backup storage.
- **Attacker:** a reader of the backup storage (often NFS/SMB, other admins, PBS users); a person who restores an old backup; a token.
- **Worst cases:**
  1. A guest backup holds the agent credential: a restore (or a copy restored elsewhere) is a second live agent for the same Target.
  2. The backup holds Plumb secrets in clear (desired list with passwords and keys), on a storage with weaker access than the guest.
  3. Plumb needs new rights (`VM.Backup`, `Datastore.AllocateSpace` on the backup storage, maybe `Datastore.Audit`/restore rights); a token can then read or restore any pool guest's data, or fill the storage.
  4. A restore over a live guest is data loss; a restore to a new VMID collides with the binding (D-320 name identity).
  5. Retention deletes backups that are not Plumb's (shared storage, same name pattern).
- **Rule:**
  - Backup of a guest is `reversible` at most; restore of a guest is `destructive` and human-only.
  - After a guest restore the agent credential of that Target is revoked and the guest re-enrolls (as D-320 does for a lost guest); one live credential per Target.
  - The plan says what secret data a vzdump holds and requires encryption (PBS client-side or a stated user choice); no default to clear text on a shared storage.
  - New rights are a user question with the full list of what they open (as D-239/D-240).
  - Retention deletes only volumes with Plumb's marker and guest id, guard like D-209.
- **Tests:** restore a guest backup (fake driver, then host) → old credential gets 401, re-enroll Plan appears; backup Plan approved by `apply` token: allowed for `reversible`, restore Plan → 403; retention with a foreign volume of the same pattern → untouched; host acceptance: grep the vzdump for the agent credential and desired list, and record the result.

## Cross-topic checks for every plan
- List new D-081 rights and new human-only reasons. Name the second path a token could take to the same power.
- Name every new place a secret can land (DB, event, log, error text, export, backup, UI) and test one leak per place.
- State the identity check (VMID + name + digest) before every write to a guest Plumb did not just make.
