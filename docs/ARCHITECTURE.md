# Architecture

Plumb is one controller service plus one small agent in every guest. This file is the contract for all agents.
Reasons for each choice: `docs/DECISIONS.md` D-018 … D-035. Terms: `docs/BRIEF.md` §3. Proxmox facts were checked against the PVE 9.2 API schema.

## 1. Overview
```
 Browser · API client · (later) MCP
        │ HTTPS :8443 — REST + JSON, SSE events — /api/v1
┌───────▼──────────── plumb (controller, unprivileged LXC) ───────────┐
│  api ─► plan · issue ─► state ─► store (one SQLite file)            │
│            │      │                                                 │
│          link   driver ─► Proxmox REST API (restricted token)       │
└────────────▲────────────────────────────────────────────────────────┘
             │ HTTPS :8444 — JSON, the agent dials out — /agent/v1
 plumb-agent (root, in every VM and LXC): observe · diff · apply · watch
```
- Go, one module, two static binaries: `plumb` and `plumb-agent` (D-018). `plumb` embeds the web UI and the agent binary.
- Web UI: Svelte 5 + TypeScript + Vite single-page app. It uses only `/api/v1`. Its API types come from the OpenAPI file (D-019).
- Storage: SQLite in WAL mode, pure-Go driver, plain SQL, forward-only migrations (D-020).

## 2. Components (`internal/<package>`)
| Package | Job | May import (plus `wire`) |
|---|---|---|
| `api` | HTTP handlers, sessions, API tokens, SSE, static UI | `plan`, `issue`, `state`, `secret`, `event` |
| `plan` | Proposal → Plan → approval → Revision → Run. Risk classes | `state`, `link`, `driver`, `secret`, `event`, `store` |
| `issue` | Drift intake, issues, resolve actions, ignore rules | `plan`, `state`, `event`, `store` |
| `state` | Documents, JSON Schema validation, Block rendering, revision diff, export/import | `resource` (schemas), `store` |
| `link` | Agent listener, Plumb CA, enrollment, command queue | `event`, `store` |
| `driver` | `Driver` interface. `driver/proxmox` (own thin REST client) and `driver/fake` (D-029) | – |
| `secret` | Encrypted secret store | `store` |
| `event` | Append-only event log and subscriptions | `store` |
| `store` | SQLite access and migrations. The only package that imports `database/sql` | – |
| `resource` | `Resource` interface, one file per type, one JSON Schema per type | – |
| `agent` | Agent loop: enroll, poll, apply, watch | `resource` |
| `wire` | Message types shared by controller and agent | – |

There are two seams (D-005): `Driver` for Proxmox in the controller, `Resource` for the guest in the agent. Only `driver/proxmox` talks to Proxmox.
```go
type Resource interface {            // internal/resource – runs in the agent
    Observe(ctx) (State, error)      // read the real state
    Diff(observed State) []Change    // Change{Attr, From, To, Risk}; applies the strictness
    Apply(ctx, []Change) error       // idempotent
    Watch() []string                 // paths whose change can mean drift
}
type Driver interface {              // internal/driver – runs in the controller
    FetchImage(ctx, ImageSpec) (ImageRef, error)       // download, verify, make template
    Create(ctx, GuestSpec, Bootstrap) (GuestID, error) // clone, set the enrollment token, start
    Observe(ctx, GuestID) (GuestState, error)          // config, config digest, power, IPs
    Diff(desired GuestSpec, observed GuestState) []Change // pure; sets the risk of each change
    Update(ctx, GuestID, []Change) error; Power(ctx, GuestID, Action) error; Delete(ctx, GuestID) error
}
```

## 3. Data model (D-021, D-026, D-027)
- The State is a set of **documents**: `kind`, `name`, `spec`. Kinds: `Image`, `Block`, `Target`, `Rule` (later).
- Documents are YAML in the export and JSON in the database. JSON Schema 2020-12 is the only schema language. It validates documents and defines Block parameters. The UI builds forms from it.
- A **Revision** is an immutable, numbered set of documents. Documents are stored once by content hash, so unchanged documents are shared. The newest revision is the desired state.
```yaml
kind: Block                          # blocks/deploy-user.yaml
name: deploy-user
spec:
  params: {required: [ssh_key], properties: {name: {type: string, default: deploy}, ssh_key: {type: string}}}  # JSON Schema
  resources:                         # values are Go text/template strings
    - user: {name: "{{ .name }}", shell: /bin/bash, ssh_keys: ["{{ .ssh_key }}"]}
---
kind: Target
name: web1
spec:
  type: vm                           # vm | lxc
  image: ubuntu-26.04
  guest: {cores: 2, memory: 2048, disk: 20, bridge: vmbr0, ip: dhcp}
  blocks: [{use: deploy-user, params: {ssh_key: "ssh-ed25519 AAAA…"}}, {use: nginx}]
  overrides: {}                      # resource id → attributes. Used by "Accept" and raw mode
```
- The controller **renders** each Target to a flat resource list. A resource id is `type:key` (for example `file:/etc/motd`). Two resources with the same id on one target are an error.
- Block values are Go `text/template` strings over the parameters. Rendering has a time limit and a size limit. Conditions, loops, Block includes and typed values come only when a real Block needs them (§10).
- Order: resources of one Block apply in the listed order. Different Blocks apply in parallel. Package-manager operations share one lock per guest.
- Restart: `service` has `restart_on: [resource ids]`. When a listed resource changes, the agent adds a `restart` change (risk `disruptive`) to the service and applies it after them. The Plan shows this change.
- Each resource type states its target kinds (`vm`, `lxc`). The Proxmox settings of a target are one resource, `guest:config`. The driver handles it, not the agent. So plans and issues have one shape for both levels.
- Tables: `doc`, `revision`, `revision_doc`, `plan`, `run`, `run_step`, `issue`, `agent`, `enroll_token`, `event`, `secret`, `user`, `session`, `api_token`.

## 4. Change flow and API (D-022, D-023)
1. A client sends a **proposal**: documents to put or delete, and a reason.
2. The controller validates and renders it. It asks the agents and the driver to observe. It stores a **Plan**: one change per resource with `action`, `changes[]` and `risk`.
3. A principal with enough rights **approves**. The controller commits the Revision (author, reason, plan id) and starts a **Run**.
4. The Run works on targets in parallel: guest step, wait for the agent, resource steps. Each step emits events.
- A Plan is stale when the head revision moved. A stale Plan cannot be approved. The agent observes again at apply time. It refuses a change with a higher risk than the approved Plan.
- Rollback, import and "Accept"/"Revert" on an issue all create a Plan. There is no second path.
- Risk classes: `read`, `reversible`, `disruptive`, `destructive`. The risk of a Plan is its highest change.

| Endpoint (`/api/v1`) | Use |
|---|---|
| `POST /session`, `DELETE /session` | Human login and logout |
| `GET /images`, `/blocks`, `/targets`, `/{kind}/{name}` | Read the desired state, plus status per target |
| `POST /plans`, `GET /plans/{id}`, `POST /plans/{id}/approve` | Propose, read, approve |
| `GET /runs/{id}` | Result per target and resource |
| `GET /revisions`, `GET /revisions/{n}/diff`, `POST /revisions/{n}/rollback` | History. Rollback returns a Plan |
| `GET /issues`, `POST /issues/{id}/resolve`, `POST /issues/{id}/comments` | Actions: `accept`, `revert`, `ignore_once`, `ignore_rule` |
| `GET /events?since=<seq>` | Server-sent events, resumable |
| `GET/POST/DELETE /tokens`, `PUT /secrets/{name}`, `GET /export`, `POST /import` | Admin |

Contract: `api/openapi.yaml` (OpenAPI 3.1, written first). Errors are `application/problem+json`. Times are UTC RFC 3339. JSON fields are `snake_case`.

## 5. Agent protocol and enrollment (D-024, D-025)
| Call (`/agent/v1`, port 8444) | Use |
|---|---|
| `POST /enroll` `{token, hostname, version}` → `{agent_id, credential}` | Once per agent |
| `GET /poll?wait=30` → `204` or `{id, type, body}` | Long poll. Types: `observe`, `apply`, `watch`, `update`, `seal` |
| `POST /report` `{type, command_id, items[]}` | Types: `result`, `progress`, `drift`, `hello` |
| `GET /binary` | Agent binary for install and update. The pinned TLS link protects it |

- The agent always dials out. A guest needs no open port. A target is online when it polled in the last 90 s.
- Port 8444 uses a certificate from the **Plumb CA** (made at first start). Agents pin this CA. Port 8443 can use any certificate.
- Enrollment token: random 256 bit, bound to one target, single use, valid 15 minutes, stored as a hash.
- **VM:** Proxmox cloud-init sets network and bootstrap user. `cicustom vendor=` points to one static snippet. The installer writes it once: root-owned, the same for all VMs, never deleted. It holds the CA, the URL and the install command, and no secret. The controller cannot write snippets and has no host mount. The token is the SMBIOS serial of the VM (`smbios1`). The agent reads it from `/sys/class/dmi/id/product_serial` (root only).
- After enrollment the agent disables cloud-init (`/etc/cloud/cloud-init.disabled`). So a later change of the cloud-init data cannot run first-boot steps again.
- **LXC:** the agent is in the template. The controller sets the token with the `env` option of the container before the first start. The agent reads it from `/proc/1/environ`. The controller then removes the option.
- After enrollment the agent sends a per-agent credential (random 256 bit, file mode 0600, stored as a hash) on every call. `seal` deletes it.
- Re-enrollment (token expired, credential lost): `POST /api/v1/targets/{name}/reenroll` creates a Plan. It sets a new token and restarts the guest (risk `disruptive`).

## 6. Drift (D-028)
- The same agent binary and the same mechanism run in VMs and LXCs. The agent keeps the desired resource list on disk, so it also checks while the controller is away.
- Fast path: inotify on the parent directory of every `Watch()` path. This includes `/var/lib/dpkg/status` (packages), `/etc/passwd`, `/etc/group` and unit directories. After a 2 s debounce the agent observes only the affected resources. The agent ignores events from its own apply and observes again after it.
- Slow path: a full check every 15 minutes. It covers what has no file event: service state, sysctl, firewall rules. Strictness is part of `Diff` (for example package `present` or `exact`), so the agent sends no noise.
- LXC differences: an unprivileged container cannot change kernel settings. `sysctl` (non-namespaced keys) and kernel modules are VM-only. Proxmox owns hostname and network of a container, so they belong to `guest:config`. Enrollment differs (§5).
- Proxmox level: the controller reads each guest config every 60 s. It compares only managed keys, and only when the config digest changed.
- A drift report opens one Issue per target and resource. Plumb never reverts or accepts by itself. "Accept" writes an override into the Target (new revision).

## 7. Security (D-030 … D-032)
- **Trust boundaries:** client ↔ controller, controller ↔ Proxmox, controller ↔ agent, guest ↔ guest. The controller is the root of trust: it has root in every managed guest. It has no root on the Proxmox host and no host directory.
- **Agents:** a credential is valid for one target only. Agent reports are untrusted input: size limits, schema check, escaped in the UI. An agent accepts commands only over the pinned TLS link.
- **Enrollment token:** it is visible in the guest config (`VM.Audit`) and to root in the guest until the next restart. This is acceptable: single use, 15 minutes.
- **Proxmox token:** user `plumb@pve`, token with privilege separation, custom role `Plumb`. Scope: pool `plumb` (`VM.Allocate`, `VM.Audit`, `VM.Clone`, `VM.Config.*`, `VM.PowerMgmt`); disk storage (`Datastore.AllocateSpace`, `Datastore.Audit`); image storage (`Datastore.AllocateTemplate`, `Datastore.Audit`); snippet storage `plumb-snippets` (`Datastore.Audit` only, content `snippets` only); one bridge (`SDN.Use`); the node (`Sys.Audit`, `Sys.AccessNetwork`). No `Sys.Modify`, no `Permissions.Modify`, no `Pool.Allocate`, no console, no guest-agent rights. The controller LXC is not in the pool, so Plumb cannot change itself or foreign guests. TLS to Proxmox trusts the `pve-root-ca.pem` of the host (the installer copies it) or the system roots.
- **Secrets:** AES-256-GCM. The API can write secrets but never read them. Documents hold references (`secret: name`). An attribute with a secret value is `sensitive`: the agent reports only a hash of it, so Plans, Issues and events never hold the value. "Accept" is not offered for it. The agent stores its desired list with mode 0600. The master key is a file on its own volume with `backup=0`, so a backup of the LXC holds the database without the key. A restore needs the recovery key. The export holds secrets only when the user gives a passphrase.
- **Humans:** local users, argon2id, session cookie (`HttpOnly`, `Secure`, `SameSite=Strict`), origin check, login rate limit, forced password change at first login.
- **API token tiers:** `read` < `plan` (propose, comment) < `apply` (approve up to the `max_risk` of the token, at most `disruptive`) < `admin`. Only a human session approves `destructive` Plans and manages tokens, secrets and import.
- **Warning:** an `apply` token is root-equivalent on the guests (a `file`, `user` or `service` change can give root). The tiers limit data loss, not privilege. The `command` resource is always `destructive`, so only a human approves it. Every Plan marks it "cannot detect drift".
- **Installer:** runs once as root on the host. It creates role, user, token, pool, the snippet storage with the static snippet, and the LXC. It verifies the tarball checksum and stores the token only inside the LXC.

## 8. Test strategy (D-034)
| Level | How | Needs |
|---|---|---|
| Unit | Resources against a temp root and a fake command runner. State, plan and issue logic | `make test` |
| Guest | Real resources inside Debian 13 and Ubuntu 26.04 systemd containers | Docker, `make test-guest` |
| Slice | Real controller + `driver/fake` + in-process agent: create, apply, drift, issue | `make test` |
| Proxmox client | Replay of recorded API answers (`testdata/pve`, tokens removed) | `make test` |
| UI | Vitest for components, Playwright against `plumb serve --dev`, `browser-shot` in every UI PR | Node |
| Real host | Installer, token scope (negative checks), image import, snippet, clone, enrollment, reboot, guest drift | `make test-pve` |

Only the real-host level needs Proxmox. It is not part of CI. **The user must provide:** a test host with Proxmox VE ≥ 9.1 (a nested VM is fine); root SSH for the one-time install; a storage with ≥ 30 GB; a bridge with DHCP or 5 free addresses; internet access from the host; permission to create and delete guests in pool `plumb-test`, VMID 9000–9099; a route from the dev VM to the host.

## 9. Conventions, layout, how to run (D-033, D-035)
- Layout: `cmd/plumb`, `cmd/plumb-agent` (flags only) · `internal/<package>` (§2, tests next to the code) · `api/openapi.yaml` · `blocks/*.yaml` (embedded) · `web/` (`web/dist` is embedded) · `install/install.sh` · `test/` (guest and real-host harness).
- Go: `gofmt`, `go vet`, `staticcheck`, `CGO_ENABLED=0`, `log/slog`, wrapped errors, `context` first, table tests. No `util` package.
- Go dependencies (a new one needs a decision line): `modernc.org/sqlite`, `go.yaml.in/yaml`, `santhosh-tekuri/jsonschema`, `golang.org/x/crypto`, `golang.org/x/sys`. No web framework, no ORM, no Proxmox SDK.
- Web: strict TypeScript, `eslint`, `prettier`, `svelte-check`. Plain CSS with tokens in one file. No component library. No hand-written API types. Web dependencies (same rule): `svelte`, `vite`, `typescript`, `openapi-typescript`, `vitest`, `@playwright/test`, and the lint tools.
- Run: `make build` (UI + both binaries) · `make test` · `make lint` · `make test-guest` · `make test-pve` · `bin/plumb serve --dev` (fake driver only, simulated agents, seeded data, plain HTTP with a cookie without `Secure`; for development and screenshots only).

## 10. Roadmap after the first slice (names only)
Secret store UI · LXC targets · image builds with Blocks, seal, retention, schedule · full installer · export/import and backups · more resources · `command` resource · template features (`when`, `each`, `uses`, `after`, typed values) · signed image checksums · Docker Compose block · Block editor · ignore rules · API token UI · agent self-update · repair fallback (SSH) · MCP · AI help on issues.

## 11. Open questions for the user (with recommendation)
The decisions behind questions 2, 4 and 5 are marked "pending" in `docs/DECISIONS.md`.
1. **Test host:** can you provide a Proxmox test host (§8)? Recommendation: a nested PVE VM used only for tests. Without it, the Proxmox driver and the installer stay untested.
2. **Proxmox version:** is "PVE 9.1 or newer only" acceptable? Recommendation: yes. LXC enrollment needs the `env` option.
3. **Exposure:** is the UI reachable only from the LAN? Recommendation: yes, with a certificate from the Plumb CA. A custom certificate comes later.
4. **Master key:** a key file on a volume that backups skip, plus a recovery key that the installer prints once (recommended), or a passphrase at every start?
5. **UI stack:** Svelte 5 (recommended, less code) or React (more widely known)? Both fit the architecture.
