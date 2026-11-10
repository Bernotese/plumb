# T-038 – LXC targets   (**blocked: test host**)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L   · **Depends on:** test host (D-041), T-012 on `main`, T-039 (template with the agent)

## Goal
A Target with `type: lxc` is created from a Plumb template, enrolls with the `env` token and is configured by the agent like a VM.

## Design (decision level – D-097, ARCHITECTURE §5, §6, §12)
- The agent is in the template. The controller checks the Proxmox base template against the checksum of the Proxmox appliance index, repacks it and uploads it through the API. No host access.
- Enrollment: `env` option before the first start, read from `/proc/1/environ`, removed after enrollment.
- `guest:config` owns hostname and network of a container. Kernel-level resources are VM-only.

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: upload limit and file names for `vztmpl`, clone type per storage, rights of the `Plumb` role for containers, the zstd dependency (own decision line).
- [ ] The card then gets files, acceptance criteria and tests. Needs new architecture decisions.

## Out of scope
- Privileged containers. Nested Docker in LXC.

## Result (owner, max 8 lines)
branch · commit · tests · notes
