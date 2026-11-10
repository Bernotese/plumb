# Decisions (one line each: ID · date · decision · why)

Later entries win over earlier ones and over `docs/BRIEF.md`. Add new decisions at the end (next: D-018).

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
