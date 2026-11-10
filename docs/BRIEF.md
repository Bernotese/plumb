# Plumb — Product Brief for AI Agents

> You are working on **Plumb**, a self-hosted infrastructure controller for a Proxmox homelab.
> This brief describes **what Plumb is, what it must do, and which decisions are already made**.
> It deliberately contains **no architecture**. Designing the architecture is the next step (owned by `pl-architect`) and must respect every decision below.
> If something here conflicts with a later entry in `docs/DECISIONS.md`, the later decision wins.

Source: user brief, 2026-11-10. Team and workflow rules are in `AGENTS.md` (not repeated here).

---

## 1. What Plumb is

Plumb replaces the usual Terraform + Ansible + Packer + CI-pipeline stack with **one long-running service** that:

1. **builds** its own VM and LXC images and keeps them up to date,
2. **provisions** VMs and LXCs on Proxmox,
3. **configures** them through a small agent running inside each guest,
4. **watches** them continuously and reports any deviation from the desired state ("drift") as an issue the user resolves.

Everything is managed through a **web interface**. No Git repository, no pipeline and no CLI are required for daily use. Internally, everything is **declarative, versioned data**.

The name comes from the plumb line: Plumb keeps your infrastructure "in plumb" and tells you when something is "out of plumb".

### The problems Plumb solves

| Pain with today's tools | Plumb's answer |
|---|---|
| Several tools and pipelines (Terraform, Ansible, Packer, CI) | One service covers image build, provisioning, configuration and drift |
| Ansible has no state; config drift happens silently | Every managed thing has a stored desired state and a known observed state |
| Ansible is slow and mostly sequential | Work runs in parallel wherever there is no dependency |
| Terraform needs lots of boilerplate without modules | Reusable **Blocks** with sensible defaults, combined in the UI |
| No native API or agent access | API-first: the web UI is just one client; AI agents are another |
| Everything depends on CI or external orchestration | Plumb is the controller itself and runs continuously |

---

## 2. Guiding principles

- **Clarity over cleverness.** A newcomer (human or AI) should understand the codebase quickly.
- **Minimal code, maximal reach.** Prefer a few general mechanisms over many special cases. Every dependency must justify itself.
- **Clear structure.** Obvious boundaries, predictable naming, one way to do each thing.
- **Declarative core.** User actions change the *desired state*; Plumb plans and reconciles. The UI never "just runs a command".
- **The UI is a client, not the system.** Anything possible in the UI is possible through the API.
- **Safe by default.** Every change goes plan → approve → apply. Destructive actions are never silent.
- **Simple, memorable naming.** Short, unique names for components and concepts. No jargon soup.

---

## 3. Vocabulary (use these terms consistently)

| Term | Meaning |
|---|---|
| **Plumb** | The controller service (runs in an LXC on the Proxmox host) |
| **Agent** | Small program inside each guest that applies and observes state |
| **Target** | A managed guest: a VM or an LXC |
| **Resource** | The smallest managed unit (a file, a package, a user, a service …). Can *observe*, *diff* and *apply* |
| **Block** | A reusable, parameterised bundle of resources (e.g. "Deploy user", "SSH hardening", "Nginx") — what the user picks in the UI |
| **Image** | A versioned VM or LXC template built by Plumb |
| **State** | The full desired configuration of everything Plumb manages |
| **Revision** | One immutable, numbered version of the state |
| **Plan** | The computed set of changes between desired and observed state, with a risk class per change |
| **Drift** | Observed state differs from desired state ("out of plumb") |
| **Issue** | A drift report the user resolves |

---

## 4. Decisions already made

These are binding. They are recorded in `docs/DECISIONS.md` as D-001 … D-017.

1. **Scope: homelab, Proxmox only.** Single Proxmox host first. No other hypervisors or clouds.
2. **Guest OS: Debian and Ubuntu only** (current Ubuntu LTS, e.g. 26.04, and Debian stable).
3. **Targets: VMs and LXCs.** Both are first-class.
4. **Built from scratch.** No Terraform, OpenTofu, Ansible or Packer under the hood. Plumb talks to the Proxmox API directly.
5. **One driver boundary.** Proxmox access and guest configuration sit behind a clean, small driver interface so new target kinds can be added later without touching the core. No other speculative abstractions.
6. **Controller runs as an unprivileged LXC** on the Proxmox host, using a **restricted Proxmox API token**, never root credentials.
7. **Installation via a shell script / tarball** on an empty Proxmox host. After that, all changes happen through Plumb.
8. **Declarative, versioned state.** Every change creates a revision with diff, history and rollback.
9. **Web UI is the primary interface.** Full management without a repository. Git is not required.
10. **Export/import of the full state** (human-readable, e.g. YAML) for backup, reproducibility and disaster recovery.
11. **Configuration through an agent** inside every guest (no SSH-based push as the main mechanism).
    - VMs: agent installed during first boot via minimal cloud-init.
    - LXCs: agent **baked into Plumb-built LXC templates** (Proxmox offers no exec API for containers).
    - Fallback for repair: Proxmox guest agent or SSH, only when the Plumb agent is unavailable.
12. **Cloud-init does the minimum** (network, bootstrap user, agent). Everything else is done by Plumb, so there is only one configuration path.
13. **Plumb builds its own images** (Packer replacement) **using the same Blocks** as normal configuration.
14. **An image is a cache, not the source of truth.** Blocks baked into an image remain part of each target's desired state and are still checked for drift.
15. **Drift becomes an Issue.** The user decides; Plumb never silently overwrites or silently accepts.
16. **API-first, agent-ready.** Plan → approve → apply for every change, risk classes, permission-scoped API tokens, an event stream and provenance on every change. An MCP interface and AI help on issues come later, on top of the same API.
17. **Out of scope for now:** Kubernetes/Talos, multi-node clusters, controller high availability, controller self-management, non-Debian/Ubuntu guests, mandatory Git workflows.

---

## 5. What Plumb must be able to do

### 5.1 Installation

- One script/tarball, run on a fresh Proxmox host.
- Creates a restricted Proxmox API token and user for Plumb.
- Creates the unprivileged Plumb LXC and starts the service.
- Ends by printing the web UI address and initial login.

### 5.2 Web interface

- Manage everything: images, targets, blocks, parameters, issues, history, settings, API tokens.
- **Create a target by selection**, for example:
  - Image: Ubuntu 26.04
  - Users: deploy user + personal user
  - Network configuration
  - Software: Nginx → with its configuration
  - System: SSH config, MOTD, shell, tools, …
- Block parameters render as forms; raw editing (e.g. a config file) is possible where it makes sense.
- Before applying: **plan preview** showing every change and its risk class.
- Live progress while applying; clear result per resource.
- History: browse revisions, view diffs, roll back.
- Issue inbox for drift.

### 5.3 State and backup

- All definitions, state data and history live inside Plumb.
- Every change is a revision with author (user, UI, API token/agent) and reason.
- Rollback to any revision (as a new plan, never blindly).
- Full export/import of the state.
- Scheduled backups (e.g. via export or Proxmox Backup Server); a fresh Plumb restored from backup can reconcile everything again.
- A small secret store for things like user passwords and SSH keys; secrets are never shown in plans, logs or exports in clear text.

### 5.4 Images and templates

- Download official Debian/Ubuntu cloud images and Proxmox LXC base templates.
- Build: start a temporary build guest, apply chosen Blocks, **seal** it (reset cloud-init, machine-id, SSH host keys, agent identity), save as a Proxmox template.
- Versioned image definitions and image versions, with a retention rule (e.g. keep last 3).
- Scheduled rebuilds (e.g. monthly with current updates).
- Each target records which image version it was created from.
- A new image version never changes existing targets; the agent keeps those current.

### 5.5 Provisioning

- Create, change, start, stop and delete VMs and LXCs from Plumb images.
- Proxmox-level settings (CPU, RAM, disks, network, tags) are part of the desired state.
- Changes to these settings go through the normal plan with risk classes (e.g. "needs reboot", "data loss possible").

### 5.6 Configuration via the agent

- Resources are **idempotent**: observe → diff → apply.
- Independent resources and independent targets are processed **in parallel**.
- Initial resource set (keep it small, extend only when needed): file, template, directory, package, apt repository, service, systemd unit, user, group, SSH key, sysctl, network, firewall, timer/cron, and a "command" escape hatch (discouraged: it cannot detect drift).
- Each resource declares which target kinds it supports; the UI hides what does not apply (e.g. kernel tuning on an unprivileged LXC).
- A Docker Compose block to cover a wide range of software without writing a block per application.
- The agent is a single, small, self-contained program; agent updates are managed by Plumb.

### 5.7 Blocks

- A Block bundles resources with typed parameters and defaults.
- Blocks are composable and reusable across targets and images.
- Ships with a starter catalogue matching the homelab basics: users, network, SSH, MOTD, shell, common tools, Nginx, Docker Compose.
- Users can create and edit their own Blocks in the UI.

### 5.8 Drift detection and issues

- **Guest level:** the agent detects changes to managed resources immediately (file-system events, package changes) plus periodic full checks.
- **Proxmox level:** changes made outside Plumb (e.g. RAM changed in the Proxmox UI) are detected via the API.
- Only **managed** resources count as drift. Unmanaged parts of the system are ignored.
- Per-resource **strictness** to avoid noise (e.g. "package present" vs. "exact version"), so automatic security updates do not flood the inbox.
- Each issue shows the diff and offers:
  - **Accept** — adopt the change into the desired state (new revision),
  - **Revert** — re-apply the desired state,
  - **Ignore once**,
  - **Ignore by rule** (permanently).
- If a change cannot be mapped back to Block parameters (e.g. a hand-edited generated config), Plumb offers to switch that resource to **raw mode** and adopt the file as-is.
- Later: AI can explain issues and suggest a resolution through the same API.

### 5.9 API and AI-agent readiness

- Everything in the UI is available through a documented API.
- Every change: **plan → approve → apply**.
- **Risk classes** per change: read-only, reversible, disruptive (restart/recreate), destructive (data loss possible).
- **API tokens with permission tiers**, e.g. an AI agent may read, plan and comment on issues, but destructive applies require human approval.
- **Event stream** (issue opened, apply finished, image built …) so clients can react instead of polling.
- **Provenance** on every change: who (human / token / agent), why, based on which plan.
- An MCP server will later be a thin layer on top of this API.

---

## 6. Open questions for `pl-architect`

Answer these in `docs/ARCHITECTURE.md` and log the result in `docs/DECISIONS.md`:

- Implementation language (Go is recommended: single static binaries for controller and agent, strong concurrency) and web UI stack.
- State storage (SQLite is recommended for simplicity and trivial backups).
- Agent ↔ controller protocol, enrollment and authentication.
- Format and schema language for Blocks and the export.
- Drift detection mechanics inside VMs vs. LXCs.
- Repository layout that keeps the code small and obvious.

---

## 7. Team and workflow

See `AGENTS.md`. One addition from the brief: **every UI change ships with a `browser-shot` screenshot in its PR.**

---

## 8. First steps

1. `pl-lead`: seed `docs/DECISIONS.md` from section 4 and create `docs/STATUS.md`.
2. `pl-architect`: answer section 6 and write `docs/ARCHITECTURE.md`, then propose the first task cards (smallest end-to-end slice first: one Debian/Ubuntu VM, created from an image, configured by the agent with a few resources, drift reported as an issue).
3. No feature code before the architecture is reviewed by `pl-reviewer` and approved by the user via `pl-lead`.
