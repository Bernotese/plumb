# T-067 – The agent reports its addresses; Issue when a guest does not use its configured address

- **Owner:** pl-architect (plan first), then tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M   · **Depends on:** –   · **State:** planning (user go for the plan: 2026-11-11); it changes the agent, so the user approves the plan before any code

## Goal
Plumb shows the addresses a guest really uses, and opens an Issue when a Target with a static address does not use it.

## Situation
- T-015 on the real host: a stock Ubuntu 26.04 guest stayed on DHCP although the Target had a static address (D-192). The agent enrolled, and Plumb showed the configured address (D-079). Nobody saw the fault until SSH failed.
- Proposal of pl-architect (D-192 discussion, point 5): Hello gets `addrs` (global unicast addresses of NICs that are up; bounded and validated like the other Hello fields). The controller stores them; `ips` shows the reported addresses when the agent is online. Static Target and configured address not in the list: Issue on `guest:config`, attribute `ip`. `ip` is create-only (D-185), so the Issue offers no revert and no accept, only ignore, and the text says "the guest does not use the configured address". Reported addresses are for display only: never for authorization, never as a connect target (root on the guest can lie).

## Context (read only these)
- `docs/DECISIONS.md` D-079, D-185, D-192, D-169 (no agent version gate yet), D-091 (ignore)
- `internal/agent` (Hello), `internal/link`, `internal/wire`, `internal/issue`, `api/openapi.yaml` (Target status `ips`)
- `docs/tasks/T-015.log.md` (the Ubuntu finding)

## Acceptance criteria (draft; the plan refines them)
- [ ] The agent sends its addresses with Hello and when they change. Bounds and validation are defined.
- [ ] The API shows reported addresses, marked as reported by the guest.
- [ ] A static Target whose address is not reported gets an Issue with a clear text; it closes by itself when the address appears. No revert, no accept.
- [ ] An older agent that sends no `addrs` gives no Issue (D-169).
- [ ] Reported addresses are never used for authorization or as a connect target. Test.
- [ ] Guest test in CI; one check on the real host with the stock Ubuntu 26.04 image (static address, first boot: Issue; after the first restart: resolved).

## Out of scope
- A fix for Ubuntu 26.04 itself (T-039).

## Plan (pl-architect, 2026-11-11; review of pl-reviewer on PR #106 is in: S1 … S4, N1 … N5)
PLAN ONLY. The user approves it before any card starts, because it changes the agent. Proposed decisions: `docs/decisions/T-067.md` (D-212 … D-217).

```
agent:  address list (netlink dump, filtered by link kind) ─► Hello.addrs   at start and after a reconnect
        list changed at a poll round, or 15 min passed      ─► report "addrs"  at most one in 30 s
link:   form check only (bounds) ─► engine: binding check of T-057 ─► agent row: addrs, addrs_at
        (latest list only; at most one stored change per agent in 30 s, from any report type)
engine: static Target + agent online + list known and complete
        address missing for 2 min ─► Issue on guest:address      address there for 2 min ─► closed
API:    TargetStatus.ips = configured, as today      reported_ips = what the guest says
```

### 1. Wire and contract
- **Hello.** `wire.Hello` gets `addrs` (list of strings) and `addrs_truncated` (boolean). A new agent always sends `addrs`, also as an empty list. A Hello **without** the field means "this agent does not report addresses" and sets the stored list to unknown, also when a list was known before (an old agent after a new one, for example a guest from an old backup).
- **New report type `addrs`** with the body `{type: "addrs", addrs: […], addrs_truncated}`. The agent sends it when the list changed. It is not a second Hello: the Run logic counts Hellos to see a restarted agent (`waitOnline`), and an address change must not look like a restart.
- **Bounds and form.** At most 16 addresses. Each one is an IPv4 or IPv6 address in canonical text (`netip.ParseAddr`, then `String()` must give the same text): no zone, no prefix length, no IPv4-mapped IPv6, no duplicate. Refused kinds: loopback, link-local, multicast, unspecified. The list is sorted (IPv4 first). The agent cuts a longer list to 16 after the sort and sets `addrs_truncated`.
- **A bad list.** In a Hello: the controller takes the Hello and stores "list unknown"; it does not refuse the Hello, so display data can never cut the link to a guest. In an `addrs` report: 400, and the agent drops the report until its next change. The log line holds the reason only, never the list (untrusted text, and addresses do not belong in logs).
- **Rate (S2).** One rule for every report type, because a Hello carries a list too and reports have no rate limit: the controller stores at most **one list change per agent in 30 s**. A change over the limit is dropped, not queued (an `addrs` report gets 429; a Hello is taken without its list). A list equal to the stored one writes nothing and emits nothing. The agent sends its list again every 15 minutes with the full check, so a dropped change heals.
- **Store (N4).** `link` checks only the form. The engine stores the list, after the binding check of T-057 (`bound()`): an agent without a Target binding has no effect. Two new columns on `agent`: `addrs` (JSON `{list, truncated}` or NULL = unknown) and `addrs_at` (time when this list arrived). Only the latest list is kept: no history, and no address in the event log. The row goes away with a re-enrollment and with the revoke of T-057. Migration 0008 (number from `pl-lead`; T-039 gets 0009. If the merge order turns, `pl-lead` renumbers before the merge).
- **API (S1).** `TargetStatus.ips` keeps its meaning: the address of the Proxmox config (D-079). It never holds a value that a guest chose, so a script that connects to `ips[0]` stays safe. New optional fields (D-100): `reported_ips` (the list; empty when the agent is offline or the list is unknown), `reported_at`, `reported_ips_more` (the guest has more than 16). A `read` token sees them, like `ips` today. `Issue.resource` can be `guest:address`; its `actions` are `ignore_once` and `ignore_rule` only. An event `target.addrs` (subject `target:<name>`, no address in it) is emitted only for a stored change.

### 2. The agent
- **Source.** Netlink dumps of links and addresses (`syscall.NetlinkRIB`, standard library; no new dependency). They give the address flags and the link kind, which `net.InterfaceAddrs` does not give.
- **Which addresses (privacy).** Interfaces that are up and not loopback. Not: loopback, link-local (`169.254/16`, `fe80::/10`), multicast, IPv6 temporary addresses (they change every day and would make a report each time), tentative, deprecated and DAD-failed addresses. Private ranges and IPv6 ULA are reported: they are the addresses of a homelab. Known and accepted: a global IPv6 address made from the MAC (EUI-64) shows that MAC; the docs say so.
- **Which interfaces (S4).** The filter is a drop list by link kind (`IFLA_INFO_KIND`), and everything else is kept. Dropped kinds: `veth`, `tun` (also tap, Tailscale), `wireguard`, `vxlan`, `geneve`, `dummy`. Dropped by name, because they are plain bridges: `docker0`, `br-` plus 12 hex characters, `cni*`, `flannel*`, `virbr*`. Kept: real cards and the guest's own `bridge`, `bond`, `vlan` and `macvlan` interfaces, so a guest with `br0` or `bond0` on its static address gets no false Issue. If nothing is left (a container: its `eth0` is a `veth`), the agent reports all interfaces that are up; T-038 makes the rule for LXC final.
- **When it sends again.** No netlink subscription. The agent reads the list again at every poll round (at most 30 s apart) and sends an `addrs` report when the list differs from the last one the controller took, and every 15 minutes; at most one in 30 s. A subscription would be faster by seconds only and adds a socket, a goroutine and a debounce to the agent.
- The agent unit already starts after `network-online.target`, so the first Hello of a healthy guest holds the configured address.

### 3. The Issue rule
- **Own resource `guest:address`,** not `guest:config` (this changes the proposal in "Situation"). Reasons: there is one Issue per Target and resource; the 60 s guest check reports `unchanged` for `guest:config` and would close the Issue; `guest:config` offers revert and accept, and here Plumb can do neither. The Proxmox config is right in this case; the guest does not follow it.
- **When the rule looks.** Only when the Target has a static `ip`, its agent is online, the list is known and not truncated, and no Run works on the Target. Else it neither opens nor closes an Issue (offline agent, stopped guest, old agent, more than 16 addresses).
- **Open:** the address has been missing from the list for the stable time of 2 minutes. Change `{attr: "ip", from: [reported list], to: "<configured address>", risk: "read"}`. Text: "The guest does not use the configured address 192.0.2.41. It reports: 192.0.2.107."
- **Close (S3):** the address has been in the list for the same 2 minutes; then `gone` by `system`. Open and close are decided in the 60 s guest loop, so each comes 2 to 3 minutes after the list changed.
- **No flapping (S3).** A new mismatch within 1 hour after a close opens the **same** Issue again and makes no new one. An Issue that was opened again stays open for at least 1 hour. With the 30 s limit of section 1 a guest that sends alternating lists gets one Issue row and about two events per hour, not hundreds per day.
- **Why 2 minutes is safe.** A booting guest sends its Hello after the network is up, so a healthy guest never has a wrong list for 2 minutes. After a create the Run is still busy with the first apply.
- **No revert, no accept.** `ip` is create-only (D-185). `Resolve` refuses both for `guest:address` (409). Ignore follows D-091 and D-092 with no change: a person or an `apply` token (the risk is `read`); "Ignore once" ends when the reported list changes; "Ignore by rule" lasts.
- **Only the Target's own data.** The check compares the configured address of a Target with the list of the same Target. Plumb draws no conclusion about another Target (no "address conflict" Issue).

### 4. Old agents and old controllers (D-169)
- An agent from before this card sends no `addrs`: the list is unknown, `reported_ips` is empty, and no Issue opens. A Hello without `addrs` also clears a list that a newer agent of the same Target left. There is no version gate and no update command; a guest gets the new agent when it is made again or re-enrolled.
- `link` refuses unknown fields. A new agent against an older controller would fail its Hello. This can happen only after a controller downgrade, because guests take the agent from their controller; the card notes it and adds no code for it.

### 5. Security
- **Display only.** A reported address is never used to authorize a call and never as a target of a connection. Checked on `main` by pl-reviewer: today no controller code dials a guest address. A later repair path (SSH) must use the configured address. `ips` stays free of guest-chosen values (S1), so the rule also holds for scripts outside Plumb.
- **A guest that lies** (root in the guest can send any list): it can hide its own mismatch or open an Issue on itself. It cannot reach another Target's Issue, status or credential. The UI marks the list "reported by the guest" and escapes it.
- **Limits:** 16 addresses, fixed form, one stored change per agent in 30 s from any report type, one Issue row per Target with at most one re-open per hour.
- **Tests for this rule:** (a) an agent that reports the address of another Target or of the controller changes nothing for that Target and gets no other right; (b) a source test (same style as the tier matrix test) fails when the stored list is read outside the status view and the address check; (c) `ips` stays the configured address while another list is reported.

### 6. Web
- Target list and Target screen: the configured address as today, and next to it the reported list with the mark "reported by the guest" ("+ more" when the guest has more than 16). A DHCP Target shows an address for the first time.
- Target screen with a mismatch: "Configured: 192.0.2.41 — the guest reports 192.0.2.107", with a link to the Issue.
- Issue screen for `guest:address`: source text "the agent in the guest", the text of section 3, a hint ("Restart the guest once. A stock Ubuntu 26.04 image takes a static address only from the second boot."), and only the two ignore actions (the screen already follows `issue.actions`).
- Mock and dev mode: one demo Target with a mismatch. `browser-shot` light and dark, Playwright.

### 7. Tests
| Level | What |
|---|---|
| Unit, agent | filter on recorded netlink dumps (temporary, tentative, link-local, loopback, `veth`, `tun`, `docker0`, **`br0` and `bond0` kept**, more than 16 with the truncated flag); "send only on change" and the 15 min send; the 30 s limit; 400 on `addrs` stops the retry |
| Unit, controller | list check (every refused form); a bad list in a Hello keeps the link and logs no address; **a Hello without `addrs` clears a known list (N1)**; **a flood of Hellos with alternating lists: at most 1 write and 1 event in 30 s (S2)**; no binding: nothing stored (N4); truncated list: no Issue (N2); stable time for open and for close with a set clock; **a flapping list for one day: one Issue row, re-open and the 1 hour rule (S3)**; busy Run; offline agent; old agent; DHCP Target; revert and accept refused; ignore once and by rule; the three security tests |
| Slice | static Target with a simulated agent that reports another address: Issue after the stable time, closed after a right list has held for 2 minutes, no Hello count change by an `addrs` report |
| Guest, in CI | Debian 13 and Ubuntu 26.04 containers: Hello holds the address of `eth0`, no loopback, no link-local; `ip addr add` and `ip addr del` give an `addrs` report in one round |
| Real host (once) | stock Ubuntu 26.04 image, static address: first boot → `ips` shows the static address, `reported_ips` the DHCP address, Issue within 4 minutes; after the first restart → Issue closed within 4 minutes, `reported_ips` shows the static address. Debian 13 static: no Issue. A DHCP Target shows its address |

### 8. Child cards (after the user's approval)
| Card | Content | Owner (proposal) | After |
|---|---|---|---|
| T-068 | Contract: `wire` (Hello.addrs, `addrs_truncated`, report `addrs`), OpenAPI (`reported_ips`, `reported_at`, `reported_ips_more`, `guest:address`), mock examples | pl-architect | – |
| T-069 | Agent: address list, link-kind filter, Hello, send on change and every 15 min; guest test | pl-backend | T-068 |
| T-070 | Controller: link form check, store after the binding check, the 30 s rule, migration 0008, engine check with the stable time and the re-open rule, status view, dev demo | pl-backend (second lane) | T-068, T-057 on `main` |
| T-071 | Web: reported list with its mark, mismatch line, Issue screen for `guest:address` | pl-frontend | T-068 (mock), T-070 (real API) |
| T-072 | QA: tests against T-069 … T-071, the security tests, the flood and flap tests, the real-host check with pl-backend | pl-codex-qa | all |

T-069, T-070 and the mock part of T-071 run in parallel. No card needs T-039.

### 9. Open questions for the user (each with one recommendation; pl-reviewer agrees with all four)
1. **How long before Plumb opens and closes the Issue?** Recommendation: 2 minutes for both. The Issue opens when the guest has not reported its configured address for 2 minutes, and it closes when the address has been there for 2 minutes. This is long enough for a slow boot and short enough to see the fault before the first SSH try.
2. **IPv6?** Recommendation: report global and ULA addresses, but no temporary (privacy) addresses. Note: a global IPv6 address can hold the MAC of the network card.
3. **Addresses of Docker, tunnel and VPN interfaces in a guest?** Recommendation: do not report them (Plumb leaves them out by the kind of the interface). Bridges, bonds and VLANs of the guest itself are reported.
4. **Which address does Plumb show?** Recommendation: `ips` always shows the configured address, as today. The list of the guest is shown next to it, marked "reported by the guest", and it is empty while the agent is offline.

## Result (owner, max 8 lines)
branch · commit · tests · notes

## Notes of pl-lead (2026-11-11)
- The plan is reviewed (PR #106). It waits for the user's approval; D-212 … D-217 stay PROPOSED until then.
- Nit of pl-reviewer for T-070: a Hello over the 30 s limit (list dropped) must not count as "no addrs field" and must not clear the stored list. Add a test.
