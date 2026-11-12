# Architecture

Plumb is one controller service plus one small agent in every guest. This file is the contract for all agents.
Reasons for each choice: `docs/DECISIONS.md` D-018 … D-049. Terms: `docs/BRIEF.md` §3. Proxmox facts were checked against the PVE 9.2 API schema.

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
| `api` | HTTP handlers, sessions, API tokens, SSE, static UI | `plan`, `issue`, `state`, `secret`, `event`, `store` |
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
    Diff(desired GuestSpec, observed GuestState) ([]Change, error) // pure; sets the risk; error for a create-only change
    Update(ctx, GuestID, []Change, *Bootstrap) error // Bootstrap only for re-enrollment; a token is never in a Change
    Power(ctx, GuestID, PowerAction) error; Delete(ctx, GuestID) error
}
```

## 3. Data model (D-021, D-026, D-027)
- The State is a set of **documents**: `kind`, `name`, `spec`. Kinds: `Image`, `Block`, `Target`, `Rule` (slice 2, D-092).
- Documents are YAML in the export and JSON in the database. JSON Schema 2020-12 is the only schema language. It validates documents and defines Block parameters. The UI builds forms from it. The schema files in `internal/` are the only source; the OpenAPI file refers to them (D-043).
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
- Order: resources of one Block apply in the listed order. Different Blocks apply in parallel. Package-manager operations share one lock per guest. A Block use has an `id` (default: the Block name); two uses of one Block need different ids (D-048).
- Order between Blocks (D-167 … D-175; contract: T-046, built by T-047 … T-051): a Block can have `spec.after: [resource ids]`, for example `after: [package:nginx]` in `nginx-site`. Each use of the Block starts only when every named resource of the same Target ended `unchanged` or `applied`. When one ended `failed`, `refused` or `skipped`, the use is `skipped` and its steps name the cause. The names are resource ids, not Block names, and the Target has no `after` field. Everything with no such relation stays parallel; list position has no meaning. Render puts the ids on the first resource of the use (`wire.Resource.after`). One function, `wire.Order`, builds the wait graph (listed order inside a use, `restart_on`, `after`) for render and for the agent. An unknown id, an id of the same use and a cycle are errors when the document is saved. The Plan shows `after` on the resource that waits.
- Restart: `service` has `restart_on: [resource ids]`. A Block parameter can set a resource key (for example a file path), so `restart_on` across Blocks names ids that depend on parameters; render checks that each id exists. When a listed resource changes, the agent adds a `restart` change (risk `disruptive`) to the service and applies it after them. The Plan shows this change. A Block can have `spec.restarts: [service ids]`: render adds the resource ids of each use of that Block to `restart_on` of the named services (D-172), so `nginx-site` restarts `service:nginx` of the `nginx` Block. The Plan names the resources that cause a restart (`restart_by`).
- Each resource type states its target kinds (`vm`, `lxc`). The Proxmox settings of a target are one resource, `guest:config`. The driver handles it, not the agent. So plans and issues have one shape for both levels.
- Tables of the first slice (migration 0001): `doc`, `revision`, `revision_doc`, `plan`, `run`, `run_step`, `issue`, `issue_comment`, `binding` (Target → guest id; no Image rows since migration 0006, D-188), `agent`, `enroll_token`, `event`, `user`, `session`, `api_token`. The table `secret` comes with the secret store, in its own migration (§10).

## 4. Change flow and API (D-022, D-023)
1. A client sends a **proposal**: documents to put or delete, and a reason.
2. The controller validates and renders it. It asks the agents and the driver to observe. It stores a **Plan**: one change per resource with `action`, `changes[]` and `risk`.
3. A principal with enough rights **approves**. The controller commits the Revision (author, reason, plan id) and starts a **Run**.
4. The Run works on targets in parallel: guest step, wait for the agent, resource steps. Each step emits events.
- A Plan is stale when the head revision moved. A stale Plan cannot be approved. The agent observes again at apply time. It refuses a change with a higher risk than the approved Plan.
- Rollback, import and "Accept"/"Revert" on an issue all create a Plan. There is no second path.
- Risk classes: `read`, `reversible`, `disruptive`, `destructive`. The risk of a Plan is its highest change.
- One vocabulary in the database, the agent protocol and the API (D-045). Ids of revisions, Plans, Runs and Issues are integers. Plan: `pending`, `approved`, `stale`. Run: `running`, `succeeded`, `failed`. Step: `pending`, `running`, `unchanged`, `applied`, `failed`, `refused`, `skipped`. Issue: `open`, `resolving`, `resolved`. A principal is `user:<name>`, `token:<id>`, `agent:<target>` or `system`.
- A change is `{attr, from, to, risk}`: `from` is the guest now, `to` is the desired state. A sensitive change is `{attr, sensitive, risk}`. The action of a resource (`create`, `update`, `delete`, `restart`) is derived from its changes by one rule, `wire.ActionOf` (D-047).

| Endpoint (`/api/v1`) | Use |
|---|---|
| `POST /session`, `DELETE /session` | Human login and logout |
| `GET /images`, `/blocks`, `/targets`, `/{kind}/{name}` | Read the desired state, plus status per target |
| `POST /plans`, `GET /plans/{id}`, `POST /plans/{id}/approve` | Propose, read, approve |
| `GET /runs/{id}` | Result per target and resource |
| `GET /revisions`, `GET /revisions/{n}/diff`, `POST /revisions/{n}/rollback` | History. Rollback returns a Plan |
| `GET /issues`, `POST /issues/{id}/resolve`, `POST /issues/{id}/comments` | Actions: `accept`, `revert`, `ignore_once`, `ignore_rule` (D-068, D-091, D-092) |
| `GET /events?since=<seq>` | Server-sent events, resumable |
| `GET/POST/DELETE /tokens`, `GET /secrets`, `PUT/DELETE /secrets/{name}`, `GET/POST /export`, `POST /import` | Admin. Token tiers per operation: `api/openapi.yaml` (§12) |
| `GET /rules`, `GET /rules/{name}` | Read the rules (D-092) |

Contract: `api/openapi.yaml` (OpenAPI 3.1, written first). Errors are `application/problem+json`. Times are UTC RFC 3339. JSON fields are `snake_case`.

## 5. Agent protocol and enrollment (D-024, D-025)
| Call (`/agent/v1`, port 8444) | Use |
|---|---|
| `POST /enroll` `{token, hostname, version}` → `{agent_id, credential}` | Once per agent |
| `GET /poll?wait=30` → `204` or `{id, type, body}` | Long poll. Types: `observe`, `apply`, `watch`, `update`, `seal` |
| `POST /report` `{type, command_id, items[]}` | Types: `result`, `progress`, `drift`, `hello`, `addrs` |
| `GET /binary` | Agent binary for install and update. The pinned TLS link protects it |

- The agent always dials out. A guest needs no open port. A target is online when it polled in the last 90 s.
- Port 8444 uses a certificate from the **Plumb CA** (made at first start). Agents pin this CA. Port 8443 also uses a Plumb CA certificate. The UI is for the LAN only (D-040).
- Enrollment token: random 256 bit, bound to one target, single use, valid 15 minutes, stored as a hash.
- **VM:** Proxmox cloud-init sets network and bootstrap user. `cicustom vendor=` points to one static snippet. The installer writes it once: root-owned, the same for all VMs, never deleted. It holds the CA, the URL and the install command, and no secret. The controller cannot write snippets and has no host mount. The token is the SMBIOS serial of the VM (`smbios1`). The agent reads it from `/sys/class/dmi/id/product_serial` (root only).
- After enrollment the agent disables cloud-init (`/etc/cloud/cloud-init.disabled`). So a later change of the cloud-init data cannot run first-boot steps again. It also has no effect in the guest. So `ip`, the bootstrap user and `disk` are create-only in the slice: a Plan that changes them is refused with a clear message (§10).
- **Images (D-188, D-189):** Plumb keeps no binding from an Image to its template. Every guest create calls `FetchImage` with the Image document of the Run's revision; the driver reuses the template of that name and checksum or makes it again. The create-only attribute `image` is the Image name, which the driver writes into the guest description, so a new checksum or a re-made template changes no guest that exists.
- **Container image versions (T-082, D-234, D-251 … D-253):** the build container comes from the repacked template file; seal runs as a container seal (the upgrade in seal, no cloud-init, no initramfs, no trim; an empty `/etc/machine-id`; a first-boot unit makes the SSH host keys before ssh; the first-boot installer and the agent unit stay, the agent binary and its state go). Before the template call the driver removes `env` and proves that the config, the next view and the pending list hold none; a Target is a full clone that must not inherit `env` and gets its own token. A clone gets no SSH keys from Proxmox: such a Target has no `ssh_keys` (use the Block `deploy-user`).
- **Image versions (D-208, D-221, D-222):** `MakeImage` clears the build values in one config write and regenerates the cloud-init drive, so a version template holds none of the build guest's user-data. Plumb makes only full clones. **Known limit (D-221):** on lvmthin the Plumb token cannot see a linked clone that someone made by hand in Proxmox, so `RemoveImage` removes the template under it; the thin clone keeps its data.
- **Known limit (D-192, D-220, D-223):** a stock Ubuntu 26.04 image used directly by a Target can sometimes stay on DHCP until its first restart due to a boot race. Use DHCP or build the Image with Plumb; built Ubuntu 26.04 Images do not have this limit. Plumb gives no warning at plan time, because an Image is only a URL and a checksum.
- **LXC:** the agent is in the template. The controller sets the token with the `env` option of the container before the first start. The agent reads it from `/proc/1/environ`. The controller then removes the option: the whole `env`, so a variable that a person adds there is removed too (Plumb owns `env` of its containers, D-231).
- **Reported addresses (D-212, D-213, D-216):** the Hello holds the agent's address list (`addrs`), and an `addrs` report sends it again when it changed (checked at every poll round, at most once in 35 s; `addrs_unknown` when it cannot read the list any more) and every 15 minutes. The agent reads it with netlink: interfaces that are up and not loopback, without tunnels, VPNs and container plumbing (`veth`, `tun`, `wireguard`, `vxlan`, `geneve`, `dummy`, GRE/IP tunnels, `docker0`, `podman0`, `lxdbr0`, `incusbr0`, `br-…`, `cni*`, `flannel*`, `virbr*`); the guest's own bridges, bonds and VLANs stay. In a container (only a `veth` left) it reports every up interface (T-038). No loopback, link-local, multicast, IPv6 temporary, tentative, deprecated or DAD-failed address; at most 16. Private ranges and IPv6 ULA are reported. Privacy note: a global IPv6 address made from the MAC (EUI-64) shows that MAC to everyone who can read the Target status. The list is display data only: never used to authorize a call or as a connect target.
- After enrollment the agent sends a per-agent credential (random 256 bit, file mode 0600, stored as a hash) on every call. `seal` deletes it.
- Re-enrollment (token expired, credential lost): `POST /api/v1/targets/{name}/reenroll` creates a Plan. It sets a new token and restarts the guest (risk `disruptive`). An agent that gets a 401 reads the token source again and enrolls again. A guest without a working agent needs the repair fallback (§10).

## 6. Drift (D-028)
- The same agent binary and the same mechanism run in VMs and LXCs. The agent keeps the desired resource list on disk, so it also checks while the controller is away.
- Fast path: inotify on the parent directory of every `Watch()` path. This includes `/var/lib/dpkg/status` (packages), `/etc/passwd`, `/etc/group` and unit directories. After a 2 s debounce the agent observes only the affected resources. The agent ignores events from its own apply and observes again after it.
- Slow path: a full check every 15 minutes. It covers what has no file event: service state, sysctl, firewall rules. Strictness is part of `Diff` (for example package `present` or `exact`), so the agent sends no noise.
- LXC differences: an unprivileged container cannot change kernel settings. `sysctl` (non-namespaced keys) and kernel modules are VM-only. Proxmox owns hostname and network of a container, so they belong to `guest:config`. Enrollment differs (§5).
- **Outside additions of a container (T-038 §3):** `guest:config` also reads `features`, mount points (`mounts`), devices (`devices`) and raw `lxc.*` lines (`raw_lxc`). Plumb sets only `features: nesting=1` (D-227); anything else is drift with risk `destructive`. "Revert" writes `features` back (active at the next start of the container). Mount points, devices and raw lines can be removed only by root on the host (host H10): a Plan for them is refused with the exact `pct set <id> --delete mp0,dev1` for mount points and devices and, for raw lines, the file `/etc/pve/lxc/<id>.conf` and the lines to remove (the API has no delete for them). "Accept" is refused for all four; "Ignore" works. **Not watched** on a container (review N1 of #169): extra NICs (`net1` and up), a `rootfs` that someone changes to another volume or a bind mount, `hookscript`, and snapshot sections (they act only after a rollback). All of them need root on the Proxmox host, which Plumb trusts (§7).
- Proxmox level: the controller reads each guest config every 60 s. It compares only managed keys, and only when the config digest changed.
- **Address check (D-215):** in the same 60 s loop the controller compares the static `ip` of a Target with the address list its agent reports (§5). It looks only when the agent is online, the list is known and not cut at 16, and no Run works on the Target. A missing address for 2 minutes opens an Issue on `guest:address` (only the ignore actions; `ip` is create-only); the address back for 2 minutes closes it as `gone`. A new mismatch within 1 hour after a close opens the same Issue again, which then stays open for at least 1 hour. The controller stores only the latest list (`agent.addrs`), at most one change per agent in 30 s, also a change to unknown (an old agent, a bad list, `addrs_unknown`); a dropped change leaves the stored list, and the next allowed change wins.
- A drift report opens one Issue per target and resource. Plumb never reverts or accepts by itself. "Accept" writes an override into the Target (new revision).

## 7. Security (D-030 … D-032)
- **Trust boundaries:** client ↔ controller, controller ↔ Proxmox, controller ↔ agent, guest ↔ guest. The controller is the root of trust: it has root in every managed guest. It has no root on the Proxmox host and no host directory.
- **Agents:** a credential is valid for one target only. Agent reports are untrusted input: size limits, schema check, escaped in the UI. The controller removes all values from a reported sensitive change before it stores it (`wire.Redact`). An agent accepts commands only over the pinned TLS link.
- **Enrollment token:** it is visible in the guest config (`VM.Audit`; a container's `env` only with `VM.Config.Options`) and to root in the guest until the next restart. This is acceptable: single use, 15 minutes.
- **Proxmox host users (T-079):** Plumb assumes no untrusted local users on the Proxmox host. An unused token is readable for at most 15 minutes by local users of the host: for a VM on the kvm command line (`/proc/<pid>/cmdline`), for a container in `/var/lib/lxc/<id>/config` (mode 0644, host fact). A user who wins the race makes the real agent's enroll fail; the re-enrollment Plan recovers.
- **Proxmox token:** user `plumb@pve`, token with privilege separation, seven roles, one per ACL path (D-081): `/pool/plumb` (`VM.Allocate`, `VM.Audit`, `VM.Clone`, `VM.Config.{CPU,Cloudinit,Disk,HWType,Memory,Network,Options}`, `VM.PowerMgmt`, `Pool.Audit`); disk storage (`Datastore.AllocateSpace`, `Datastore.Audit`); image storage `plumb-images`, content `import,vztmpl` (D-229; `Datastore.AllocateTemplate`, `Datastore.Audit`); snippet storage `plumb-snippets`, content `snippets` only (`Datastore.AllocateSpace`, needed by `cicustom`); one bridge (`SDN.Use`); the node (`Sys.AccessNetwork`); `/` with propagate 0 (`Sys.Audit`, role `PlumbAudit`, D-240): the thin-pool metadata use for the disk gate (`GET /nodes/<node>/disks/lvmthin` checks `Sys.Audit` on the fixed path `/`, so no narrower path works). Not inherited, so it gives no `/nodes/<node>`, guest-config or storage read. It still lets the token read (never write), cluster-wide: the task list of all users (`/cluster/tasks`); the use metrics of every guest, node and storage (`/cluster/metrics/export`); the guests (IDs, names, volumes) in backup jobs (`included_volumes`) and the backup jobs; the node disk lists and SMART data, the PCI list, `/etc/hosts` and the node config; all datacenter options, cluster status and config, HA, metric-server config (not its token), cluster firewall options, rules, security groups, aliases and IP sets, realm sync jobs, Ceph status. So Plumb sees the IDs, names and use of all guests and the tasks of all users, but no config of a guest outside the pool. An install from before D-240 gets it in a repair run; until then the Plan says "metadata use unknown". No other ACL. No `Sys.Modify`, no `Sys.Audit` on any other path, no `Permissions.Modify`, no `Pool.Allocate`, no console, no guest-agent rights. The disk storage has no `snippets` content; Proxmox needs qemu-server ≥ 9.2.11 and libpve-storage-perl ≥ 9.1.13. The controller LXC is not in the pool, so Plumb cannot change itself or foreign guests. TLS to Proxmox trusts the `pve-root-ca.pem` of the host (the installer copies it) or the system roots.
- **Secrets:** AES-256-GCM. The API can write secrets but never read them. Documents hold references (`secret: name`). An attribute with a secret value is `sensitive`: the agent compares it locally and reports only "changed" or "not changed", so Plans, Issues, events and the database never hold the value or a hash of it. "Accept" is not offered for it. The agent stores its desired list with mode 0600. The master key is a file on its own volume with `backup=0`, so a backup of the LXC holds the database without the key. A restore needs the recovery key. The Plumb CA key is in the normal backup; a new CA needs a new snippet (`install.sh --snippet-only`) and re-enrollment of every agent. The export holds secrets only when the user gives a passphrase.
- **Image build `seal` (D-205 … D-207, T-060):** two independent guards. The controller sends `seal` only to a link `build:<name>`; the agent seals only with the build mark (taken only at a first enrollment from an empty state) and without a `watch` mark (never cleared). Both marks are in the credential file, and no `file`, `directory`, `symlink`, `user` or `service` resource may touch the agent's own paths or unit, also through a link in the guest. **Known limit (n4):** the controller cannot prove that `seal` ran. Root in the build guest holds the build credential and can send a false result; the controller trusts root in a guest already. The real-host check (T-066) searches the template disk.
- **Humans:** local users, argon2id, session cookie (`HttpOnly`, `Secure`, `SameSite=Strict`), origin check, login rate limit, forced password change at first login.
- **API token tiers:** `read` < `plan` (propose, comment) < `apply` (approve up to the `max_risk` of the token, at most `disruptive`). `admin` is the tier of a human session; no token has it (D-046). Only a human session approves `destructive` Plans and manages tokens, secrets and import.
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
- Web: strict TypeScript, `eslint`, `prettier`, `svelte-check`. Plain CSS with tokens in one file. No component library. No hand-written API types. Web dependencies (same rule): `svelte`, `vite`, `typescript`, `openapi-typescript`, `vitest`, `@playwright/test`. Build and lint tools are free as `devDependencies` (D-042).
- Upgrade (D-229): run the new `install.sh` with the new `plumb` binary, as a repair run (`install.sh --plumb-binary bin/plumb`; `--dry-run` first). An install from before D-229 has `plumb-images` with content `import` only: the controller starts with a warning, VM Targets work, LXC Images fail with "run install.sh again". The repair run shows the change `import → import,vztmpl`, asks, pushes the new binary, then sets the content, then restarts `plumb.service`. Do not change the storage by hand before the new binary is in: an older binary refuses `import,vztmpl` at its start (exit 78). `pvesm set` rewrites `/etc/pve/storage.cfg` and reorders the content lists of other storages (same content). When the new binary does not start, the installer sets the content back to `import` and says so.
- Rollback to an older binary by hand: run `pvesm set plumb-images --content import` first, then put the old binary back.
- Run: `make build` (UI + both binaries) · `make test` · `make lint` · `make test-guest` · `make test-pve` · `bin/plumb serve --dev` (fake driver only, simulated agents, seeded data, plain HTTP with a cookie without `Secure`; for development and screenshots only).

## 10. Roadmap after the first slice (names only; slice 2 plan: §12)
Secret store UI · LXC targets · image builds with Blocks, seal, retention, schedule · full installer · export/import and backups · more resources (`network`, disk grow with guest resize) · `command` resource · template features (`when`, `each`, `uses`, typed values; `after` is in §3) · signed image checksums · Docker Compose block · Block editor · ignore rules · API token UI · agent self-update (it owns the protocol version gate: an agent from before D-169 ignores `after`, D-176) · repair fallback (SSH) · MCP · AI help on issues.

## 11. User answers (2026-11-10) and open points
1. **Test host:** it comes later, on 2026-11-11 at the earliest. Real-host work (T-012, T-013, real-host part of T-015) is blocked until `pl-lead` confirms the host (D-041). **This is the only open point.**
2. **Proxmox version:** PVE 9.1 or newer only (D-032).
3. **Exposure:** LAN only, with a certificate from the Plumb CA (D-040).
4. **Master key:** a key file on a volume that backups skip, created at install. The installer shows a recovery key once. No passphrase at start (D-030).
5. **UI stack:** Svelte 5 (D-019).

## 12. Slice 2 (D-085 … D-099)
**Scope, part A – build and verify now** (no Docker, no Proxmox host; tests use `driver/fake` and the simulated agents of `link/linktest`):
API tokens · revision history with diff and rollback · secret store · "Ignore once" and "Ignore by rule" · export/import.
**Part B – design only, cards `blocked: test host`:** LXC targets (D-097) · image builds with seal, retention, schedule (D-098).
**Later (not planned here):** Docker Compose block, Block editor, MCP, AI help, agent self-update, repair fallback, backups, guest adoption after a restore.

What slice 1 already has: token tiers and `api.CreateToken` without an HTTP route; `store.Revisions`, `Diff`, `RevisionDocs` without a route; `wire.Redact`, `Resource.Sensitive` and the masking in `link` without a source of secrets; an empty package `secret`. It has no rollback, no export, no `Rule` kind and no UI for any of these.

| Order | Cards | Why this place |
|---|---|---|
| 1 | T-027 API contract | Backend and UI then work in parallel. The UI starts on the mock (as in slice 1) |
| 2 | T-028 secret store → T-029 secret references (`pl-backend`) | Largest item and the security risk: it starts first and gets the longest review |
| 2 | T-030 token API, T-031 revision API (second backend lane) | Small. The store code exists. They unblock two UI screens at once |
| 2 | T-034 history UI, T-035 settings UI (`pl-frontend`) | Need only the contract |
| 3 | T-032 ignore actions with the `Rule` kind | Needs no agent change. After the secret cards in the backend lane |
| 3 | T-036 secret parameters and ignore actions in the UI | Needs the shapes of T-029 and T-032 |
| 4 | T-033 export/import | Must cover every kind (`Rule`) and the secret bundle, so it is last |
| 5 | T-037 leak hunt and slice 2 acceptance (`pl-codex-qa`) | Independent proof over the whole flow |
| – | T-038 LXC targets, T-039 image builds | Blocked: test host. T-039 comes first: an LXC target needs a template with the agent |

Rule for the order: a card is in part A only when its acceptance criteria can be checked with `make lint test` and Playwright on `plumb serve --dev`.

**Secret store (D-086 … D-090, D-099)**
```
key file (32 bytes, own volume, not in backups) ── recovery key = the same bytes, shown once on a terminal
   │   made only by `plumb key init` (installer); `serve` never makes a key and stops without one
   └─ HKDF-SHA256 per purpose ─► secret-store key ─► AES-256-GCM per secret (table `secret`)
Block:   params: {properties: {db_password: {type: string, writeOnly: true}}}
Target:  blocks: [{use: app, params: {db_password: {secret: app-db}}}]
render:  file:/etc/app.env  attrs.content = "DB_PASSWORD=<marker app-db>\n"   sensitive: [content]
plan:    replaces markers with values only in observe / apply / watch command bodies
agent:   compares locally, reports {attr: content, sensitive: true, risk}; desired list has mode 0600
```
- Clear values exist in three places only: controller memory, the pinned TLS link, the guest. The database holds ciphertext. Documents, Plans, the render cache, run steps, events, logs and API responses hold names or markers.
- The redaction list of `link` (D-062) is `Resource.Sensitive` of the head render. The render fills it from the markers.
- A template can only print a secret parameter. It cannot cut, compare, measure or forge it: the render accepts only markers that it made.
- A new secret value reaches a guest only through a Plan for the targets that use it (`Proposal.targets`). A Plan records the secret versions it was planned with; another version makes it stale, a missing one fails the Run.
- **Only a human approves** a Plan that gives a secret to a target and resource that did not have it, for every origin (D-099). So a token cannot read a secret by sending it to a guest.
- The export holds secret values only inside a passphrase-encrypted bundle (D-094). An import creates missing secrets only; it overwrites a live value only when the request names it (D-095).

**Other part A items**
- **Tokens:** list, create (bearer shown once), revoke. Human session only. Inside D-046.
- **History:** list, diff of two revisions, rollback as a Plan (D-093).
- **Ignore once:** resolves the Issue without a Plan and remembers its changes. A human can do it, or an `apply` token within its `max_risk` and never for a sensitive change; a `plan` token cannot (D-091).
- **Ignore by rule:** a `Rule` document through a Plan. Only a human approves any `Rule` change (D-099). The controller filters drift at intake and shows per target what it dropped (D-092).
- **Export/import:** one YAML stream of the head revision; import is a Plan with mode `replace` or `merge` (D-094, D-095).
- **Migrations:** 0003 secrets, 0004 Plan origins, 0005 Issue resolution `ignore`. Merge in this order (D-096).

**Part B at decision level**
- **Image builds:** `Image` gets `blocks`, `retention.keep`, `schedule`. Build = Run on a temporary guest → `seal` → template → image version. A build never changes a target and uses no secrets.
- **LXC targets:** the controller checks the base template against the Proxmox checksum, repacks it with the agent inside and uploads it through the API. Enrollment uses the `env` token (§5). No host access.

Questions for the user: `docs/tasks/T-025-slice-2-plan.md`.

## 13. LXC Images: the pin (D-224, D-235)
An `lxc` Image names a Proxmox system template by URL and pins its SHA-512. Proxmox serves the templates over http only; the pin is the trust. Copy the pin from the `sha512sum` field of the signed Proxmox appliance index, never from the downloaded file. Creating an `lxc` Image or changing its `url` or `sha512` needs a human approval (`image-recipe`).

Supported templates (index `aplinfo-pve-9.dat`, signature checked against the Proxmox Trixie Release Key on 2026-11-12):

| Template | `url` | `sha512` |
|---|---|---|
| Debian 13 | `http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst` | `4c0c27ca6ceab5ef0b84db57825a00f26157ef1854bafe97297813e1cbe8ecb8cc9c453cab6b3b0efe1ba193a50c47ece1e41d950e411b8730b835b71e9e754b` |
| Ubuntu 26.04 | `http://download.proxmox.com/images/system/ubuntu-26.04-standard_26.04-1_amd64.tar.zst` | `d5607f124d01f8ddfebd8e7da34c0022fb2a464e1662ee1e39a13f5ed1bae08a7364c486247fbd9f69f26c710729884ee5047aadb89f33a40066cd5f0d9def88` |

Other templates and newer versions: on the Proxmox host, `pveam update` downloads the index and checks its signature; then this command prints the `url` and `sha512` of each template from the checked index cache:

```sh
pveam update && awk '/^Location:/{l=$2} /^sha512sum:/{s=$2} function p(){if(l)print "http://download.proxmox.com/images/" l, s; l=""} /^$/{p()} END{p()}' /var/lib/pve-manager/apl-info/download.proxmox.com
```

When Proxmox removes an old version from its mirror, the download fails with 404: run the command again and update the Image.
