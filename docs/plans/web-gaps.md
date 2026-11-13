# Web UI gaps against BRIEF §5.2 (T-141)

State of `main` at 71c14c0 (2026-11-13). Checked with the running app (`bin/plumb serve --dev --dev-demo`,
`browser-shot` of every screen named below) and against `web/src/` and `api/openapi.yaml`.
Input for T-138 (next slices). No code changes.

Legend: **state** done / partly / missing · **API** = needs a new or changed endpoint · **size** S (≤1 day,
one screen) / M (one screen + client + tests) / L (several screens or server work).

## 1. Gap table

| # | Brief line | State | Where (route, component) | What is missing | API | Size |
|---|---|---|---|---|---|---|
| 1 | §5.2 Manage … **settings** | partly | `#/settings/*` `Settings.svelte` (tabs: tokens, secrets, export, backups, users) | No controller settings at all. Backup schedule and `keep` are read-only text in `Backups.svelte` (set outside the UI). No view of the Proxmox connection (node, pool, storages) or of the agent update policy. | yes (settings resource, at least `PUT` backup schedule/keep) | M |
| 2 | §5.2 Manage … **API tokens** | done | `#/settings/tokens` `Tokens.svelte` | Create (name, tier, max risk), list, revoke, enable after restore all work. Small: no expiry, no "last used from". Token page itself is not a gap. | no | – |
| 3 | §5.2 Manage … (users; D-326 "user management") | missing | `#/settings/users` `Settings.svelte` (one form) | Only "give a one-time password" to a user name typed by hand (`POST /users/{name}/password`). No user list, no create, no delete/disable, no role. The admin cannot see which users exist. | yes (`GET/POST /users`, `DELETE /users/{name}`; roles need a decision) | L |
| 4 | §5.2 / §5.3 **History**: browse revisions | done | `#/history` `History.svelte` | Paged list with author, reason, plan link. Small: no filter by document (e.g. "all revisions of Target web1"). | no (filter: yes, small) | S |
| 5 | §5.2 / §5.3 **History: view diffs** | partly | `#/revisions/{n}` `RevisionView.svelte`, `DocDiff.svelte` | Shows only n-1 → n. API `GET /revisions/{n}/diff?from=` is not used: no "compare with head" or "compare any two" (needed to judge a rollback). | no | S |
| 6 | §5.2 / §5.3 **History: roll back** | done | `#/revisions/{n}` rollback form → Plan (origin `rollback`) | Works (reason required, makes a Plan). | no | – |
| 7 | §5.2 **raw editing** where it makes sense | partly | `TargetForm.svelte` → `RawResources.svelte`; `BlockForm.svelte` (YAML); `SchemaFields.svelte` textarea for `contentMediaType: text/plain` | Raw resources per Target: done. Block YAML: done. Missing: (a) a parameter of type object / array of objects shows "This parameter type needs raw mode (later)" (`SchemaFields.svelte:118`) and cannot be set at all; (b) no YAML view/edit of a Target or Image document (only forms). | no | M |
| 8 | §5.7 **Block create/edit** in the UI | done (YAML only) | `#/blocks/new`, `#/blocks/{n}/edit` `BlockForm.svelte`, `BlockView.svelte` | Create/edit as YAML with client check + server check, "used by" list, plan. Missing: **delete a Block** (API: Proposal `delete`), no guided editor for params (YAML is accepted per brief). | no | S (delete) |
| 9 | §5.7 **starter catalogue** visible in the UI | missing | `#/blocks` `Blocks.svelte` | `blocks/*.yaml` (12 starter Blocks) are embedded in no binary path: no non-test Go code imports `blocks`. A fresh install shows an empty Block list; README step 5 says "look at the starter Blocks". dev-demo only has motd, nginx, nginx-site. Need "Add from catalogue" (list + one-click put Plan). | yes (`GET /catalog/blocks` from `blocks.FS`), or no if the web bundles the YAML (worse: two copies) | M |
| 10 | §5.2 **parameter forms** | done | `SchemaFields.svelte`, `lib/schemaForm.ts` (text, integer, number, boolean, enum, list of strings, textarea, secret picker) | Works on Target and Image forms. Gap only for object/array-of-object params (see #7a). | no | – |
| 11 | §5.2 **Create a target by selection** (image, users, network, software, system) | partly | `#/targets/new` `TargetForm.svelte` | Image, size, power, tags, bootstrap user, SSH keys, Blocks (with use id for repeated use, unfit Blocks hidden per kind): done. **Network**: one bridge + IPv4/gateway, create-only, one NIC, no VLAN; changing IP later is impossible. Users: only via `deploy-user` Block twice (works, but no "personal user" hint). | yes (network resource / more NICs, D-326 list) | L |
| 12 | §5.5 delete a VM/LXC | missing | `Targets.svelte`, `TargetForm.svelte` | No "Delete Target" button anywhere. API supports it (Proposal `delete: [{kind: Target}]`, D-325). Same for **Image delete** (`ImageView.svelte`). Only Rules have delete. | no | S |
| 13 | §5.2 **plan preview** with risk class | partly | `#/plans/{id}` `PlanView.svelte` | Resource changes + risk per change + plan risk: done. Missing: (a) **no list of plans**: a pending plan (dev-demo Plan 2) is reachable only by its URL; `GET /plans` is unused; (b) **no "Drop plan"**: "Not now" only links to targets, `DELETE /plans/{id}` unused, so pending plans pile up; (c) plan does not show which documents change (only resource effects); a Block edit is hard to review. | (a)(b) no · (c) yes (documents/diff in Plan) | M |
| 14 | §5.2 **Live progress**, result per resource | partly | `#/runs/{id}` `RunView.svelte` (live via event stream) | Per-resource status and output: done. Missing: **no run list** (`GET /runs` unused); the only way to a Run is the link after approve or from a Plan. | no | S |
| 15 | §5.2 / §5.8 **Issue inbox** list | done | `#/issues` `Issues.svelte` (live) | Status filter works. Small: no target filter (API `?target=` unused), no link from a Target row to its open issues. | no | S |
| 16 | §5.8 Issue: show the diff | done | `#/issues/{id}` `IssueView.svelte`, `ChangeTable.svelte` | Attribute / guest / desired / risk. | no | – |
| 17 | §5.8 Issue actions: Accept, Revert, Ignore once, Ignore by rule | partly | `IssueView.svelte`, `#/issues/rules` `Rules.svelte` | Revert, ignore once, ignore by rule (target/all, all/changed attributes), rule delete: done. **Accept** on a Block resource is always "Accept as raw for this Target" (D-068 overrides): Plumb never maps the change back to a **Block parameter** (e.g. motd text → param `text`), which the brief puts before raw mode. | yes (server: param mapping in accept) | L |
| 18 | §5.8 / §5.9 issue comments (AI may comment) | partly | `IssueView.svelte:298` | Comments are shown, but a person cannot add one: `POST /issues/{id}/comments` is unused. | no | S |
| 19 | §5.3 export/import, secrets, backups | done | `#/settings/export`, `#/settings/secrets`, `#/settings/backups` | Work (export with passphrase, import → Plan; secret put/delete/used-by; backup now, list, restored mode). Backup schedule edit: see #1. | – | – |
| 20 | §5.4 images: versions, schedule, retention | done | `#/images*` `ImageForm.svelte`, `ImageView.svelte` | Build, versions, schedule, retention: done. Delete: see #12. | no | – |

## 2. API features that the UI shows only half

- `GET /plans`, `DELETE /plans/{id}` – unused (#13).
- `GET /runs` – unused (#14).
- `POST /issues/{id}/comments` – comments read-only (#18).
- `GET /revisions/{n}/diff?from=` – only n-1 (#5).
- `GET /issues?target=` – unused (#15).
- Proposal `delete` – only for Rules; not for Target, Block, Image (#8, #12).
- `GET /rules/{name}` – unused; list is enough (no gap).

## 3. Proposed order (recommendation for T-138)

1. **Web-only slice (no API, S each, one card):** #12 delete Target/Block/Image, #13a+b plan list and drop,
   #14 run list, #18 add comment, #5 compare revisions, #15 target filter. All checkable with `make test`,
   Playwright on `serve --dev`.
2. **Starter catalogue (#9):** small API + Blocks page section. High value: a fresh install is empty today.
3. **Users (#3):** needs a decision on roles first (pl-architect); API + screen.
4. **Settings (#1):** backup schedule edit first; other settings when the server has them.
5. **Large, server-led:** network/NICs (#11), accept-to-parameter (#17), plan document diff (#13c),
   object params (#7a).

## 4. Risks / notes

- #9 is a product bug more than a gap: README and BRIEF promise starter Blocks in the UI.
- #13b: without drop, a stale pending Plan stays forever; check whether the server marks it `stale`
  only on a new revision.
- #3 roles may touch D-081 only if they map to Proxmox rights (they should not); token rights stay as they are.
- Screens checked: targets, target new/edit, blocks, block view/new, issues, issue 2/3, rules, history,
  revision 1, plan 2, run 1, settings tokens/users. Screenshots are local only (not committed).
