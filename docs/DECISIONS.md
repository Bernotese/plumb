# Decisions (one line each: ID · date · decision · why)

Later entries win over earlier ones and over `docs/BRIEF.md`. Add new decisions at the end (next: D-036).

- D-001 · 2026-11-10 · Scope is a homelab with one Proxmox host; no other hypervisors or clouds · user brief §4.1
- D-002 · 2026-11-10 · Guest OS is Debian stable and current Ubuntu LTS only · user brief §4.2
- D-003 · 2026-11-10 · Targets are VMs and LXCs, both first-class · user brief §4.3
- D-004 · 2026-11-10 · Built from scratch: no Terraform, OpenTofu, Ansible or Packer; Plumb talks to the Proxmox API directly · user brief §4.4
- D-005 · 2026-11-10 · One small driver interface hides Proxmox access and guest configuration; no other speculative abstractions · user brief §4.5
- D-006 · 2026-11-10 · Controller runs in an unprivileged LXC on the Proxmox host with a restricted API token, never root credentials · user brief §4.6
- D-007 · 2026-11-10 · Installation is one shell script / tarball on an empty Proxmox host; all later changes go through Plumb · user brief §4.7
- D-008 · 2026-11-10 · State is declarative and versioned; every change creates a revision with diff, history and rollback · user brief §4.8
- D-009 · 2026-11-10 · Web UI is the primary interface; Git is not required · user brief §4.9
- D-010 · 2026-11-10 · Full state export/import in a human-readable format (e.g. YAML) · backup and disaster recovery, user brief §4.10
- D-011 · 2026-11-10 · Configuration runs through an agent in every guest. VM: installed at first boot via minimal cloud-init. LXC: baked into Plumb-built templates. Proxmox guest agent or SSH only as repair fallback · user brief §4.11
- D-012 · 2026-11-10 · Cloud-init does the minimum (network, bootstrap user, agent) · one configuration path, user brief §4.12
- D-013 · 2026-11-10 · Plumb builds its own images with the same Blocks as normal configuration · Packer replacement, user brief §4.13
- D-014 · 2026-11-10 · An image is a cache, not the source of truth; baked Blocks stay in each target's desired state and are checked for drift · user brief §4.14
- D-015 · 2026-11-10 · Drift becomes an Issue; Plumb never silently overwrites or accepts · user brief §4.15
- D-016 · 2026-11-10 · API-first: plan → approve → apply for every change, risk classes, permission-scoped API tokens, event stream, provenance. MCP and AI help come later on the same API · user brief §4.16
- D-017 · 2026-11-10 · Out of scope for now: Kubernetes/Talos, multi-node clusters, controller HA, controller self-management, non-Debian/Ubuntu guests, mandatory Git workflows · user brief §4.17
- D-018 · 2026-11-10 · Go for controller and agent: one module, two static binaries (`plumb`, `plumb-agent`), `CGO_ENABLED=0` · brief recommendation; single-file deploy, strong concurrency, one language for both sides
- D-019 · 2026-11-10 · Web UI is a Svelte 5 + TypeScript + Vite single-page app, embedded in `plumb`; it uses only `/api/v1`; API types are generated from the OpenAPI file · proves "the UI is a client"; little boilerplate; server-rendered HTML would add a second, non-API surface
- D-020 · 2026-11-10 · SQLite (WAL, one file) with `modernc.org/sqlite`, plain SQL, forward-only embedded migrations · brief recommendation; trivial backup; pure Go keeps the binary static; no ORM to learn
- D-021 · 2026-11-10 · State is a set of documents (`kind`, `name`, `spec`); a revision is an immutable set of content-addressed documents; the head revision is the desired state · cheap diff, rollback and export from one mechanism
- D-022 · 2026-11-10 · One change flow: proposal → Plan → approve → Revision + Run. A Plan is stale when the head moved. The agent re-observes at apply and refuses a higher risk than approved · no change without a reviewed plan; rollback, import and issue actions reuse the same path
- D-023 · 2026-11-10 · Public API is REST + JSON under `/api/v1`, OpenAPI 3.1 written first, events as SSE, errors as problem+json · standard tools, stdlib only, easy base for MCP later
- D-024 · 2026-11-10 · Agent protocol is HTTPS + JSON; the agent dials out; long poll for commands, POST for reports; own port 8444 with a Plumb CA certificate that agents pin · no open port in guests, stdlib only, every message is an authenticated request; gRPC/WebSocket add dependencies without a need at homelab scale
- D-025 · 2026-11-10 · Enrollment uses a one-time token (bound to one target, single use, 15 min). VM: cloud-init vendor-data snippet. LXC: container `env` option, read from `/proc/1/environ`. Then a per-agent bearer credential, stored as a hash · the token never travels over the network before TLS is pinned; a bearer credential is simpler than mTLS with equal protection here
- D-026 · 2026-11-10 · YAML for Blocks and the export; JSON Schema 2020-12 as the only schema language; Go `text/template` for parameter values and file templates · one schema language for validation, UI forms and OpenAPI; all three are standard or stdlib; CUE/HCL/own DSL cost more to learn
- D-027 · 2026-11-10 · The controller renders each target to a flat list of resources (`type:key`); the agent runs observe/diff/apply/watch; listed order inside a Block, Blocks in parallel; target `overrides` implement "Accept" and raw mode · few general mechanisms, predictable order, parallel by default
- D-028 · 2026-11-10 · Drift: same agent in VM and LXC; inotify on parent directories plus a full check every 15 min; strictness lives in `Diff`; Proxmox level by polling the config digest every 60 s; kernel-level resources are VM-only · immediate for files and packages, complete by the periodic check, no noise from the source
- D-029 · 2026-11-10 · One `Driver` interface (six methods) with `driver/proxmox` (own thin REST client) and `driver/fake`; Proxmox settings of a target are the resource `guest:config` · D-005; about 20 endpoints do not justify an SDK; the fake makes the whole flow testable
- D-030 · 2026-11-10 · Secrets: AES-256-GCM, master key file outside the database, write-only API, references in documents, masked in diffs/logs/events, export only with a passphrase · a stolen database or export leaks no secret; stdlib crypto only
- D-031 · 2026-11-10 · Humans: local users, argon2id, strict session cookie. API tokens: tiers `read` < `plan` < `apply` (with `max_risk`, at most `disruptive`) < `admin`; only a human session approves `destructive` · brief §5.9; an AI client can plan but cannot destroy
- D-032 · 2026-11-10 · Proxmox access: user `plumb@pve`, token with privilege separation, role `Plumb`, limited to pool `plumb`, chosen storages, one bridge, node audit. Minimum Proxmox VE 9.1 · least privilege (D-006); the controller cannot change itself or foreign guests; 9.1 has the LXC `env` option
- D-033 · 2026-11-10 · Repo layout: `cmd/`, `internal/<package>` (12 packages with a fixed import direction), `api/`, `blocks/`, `web/`, `install/`, `test/` · one obvious place for each thing
- D-034 · 2026-11-10 · Tests: unit with temp root and fake runner; guest tests in Debian/Ubuntu containers; slice tests with `driver/fake` and an in-process agent; recorded Proxmox answers; real-host suite outside CI · the team has no Proxmox host in CI
- D-035 · 2026-11-10 · Conventions: gofmt, go vet, staticcheck, slog, strict TypeScript, eslint, prettier, svelte-check. Go dependency allowlist: `modernc.org/sqlite`, `go.yaml.in/yaml`, `santhosh-tekuri/jsonschema`, `golang.org/x/crypto`, `golang.org/x/sys`; a new dependency needs a decision line · brief §2 "every dependency must justify itself"
