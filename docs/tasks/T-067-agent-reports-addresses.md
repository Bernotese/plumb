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

## Plan (pl-architect, 2026-11-11)
PLAN ONLY. The user approves it before any card starts, because it changes the agent. Proposed decisions: `docs/decisions/T-067.md` (D-212 … D-217).

```
agent:  address list (netlink dump, filtered) ─► Hello.addrs            at start and after a reconnect
        list changed at a poll round          ─► report type "addrs"    at most one in 30 s
link:   check (bounds, form) ─► agent row: addrs, addrs_at (latest list only)
engine: static Target + agent online + list known + address not in the list for 2 min
        ─► drift item for the resource guest:address ─► issue.Intake (rules, ignore, open, close)
API:    TargetStatus.ips = reported list, ips_source: reported | configured
```

### 1. Wire and contract
- **Hello.** `wire.Hello` gets `addrs` (list of strings). A new agent always sends the field, also as an empty list. A missing field means "this agent does not report addresses".
- **New report type `addrs`** with the body `{type: "addrs", addrs: […]}`. The agent sends it when the list changed. It is not a second Hello: the Run logic counts Hellos to see a restarted agent (`waitOnline`), and an address change must not look like a restart.
- **Bounds and form.** At most 16 addresses. Each one is an IPv4 or IPv6 address in canonical text (`netip.ParseAddr`, then `String()` must give the same text): no zone, no prefix length, no IPv4-mapped IPv6, no duplicate. Refused kinds: loopback, link-local, multicast, unspecified. The list is sorted (IPv4 first). The agent cuts a longer list to 16 after the sort.
- **A bad list.** In a Hello: the controller takes the Hello and stores "list unknown" (one log line); it does not refuse the Hello, so display data can never cut the link to a guest. In an `addrs` report: 400, and the agent drops the report until its next change.
- **Rate.** The controller writes only when the list differs from the stored one. `link` takes one `addrs` report per agent in 10 s (429 for more).
- **Store.** Two new columns on `agent`: `addrs` (JSON list or NULL = unknown) and `addrs_at` (time when this list arrived). Only the latest list is kept: no history, and no address in the event log. The row goes away with a re-enrollment and with the revoke of T-057. Migration number: `pl-lead` gives it (T-039 plans one too).
- **API.** `TargetStatus.ips` keeps its name. It holds the reported list when the agent is online and the list is known; else the configured static address as today (D-079). New optional fields (D-100): `ips_source` (`reported` | `configured`) and `ips_reported_at`. `Issue.resource` can be `guest:address`; its `actions` are `ignore_once` and `ignore_rule` only. An event `target.addrs` (subject `target:<name>`, no address in it) tells the UI to read again.

### 2. The agent
- **Source.** One netlink address dump (`syscall.NetlinkRIB`, standard library; no new dependency). It gives the flags that `net.InterfaceAddrs` does not give.
- **Which addresses (privacy).** Interfaces that are up and not loopback. Not: loopback, link-local (`169.254/16`, `fe80::/10`), multicast, IPv6 temporary addresses (they change every day and would make a report each time), tentative, deprecated and DAD-failed addresses. Private ranges and IPv6 ULA are reported: they are the addresses of a homelab.
- **Which interfaces.** Only real network cards: an interface that is not under `/sys/devices/virtual/net`. So Docker bridges, `veth`, WireGuard and Tailscale addresses are not reported. If a guest has no such interface (a container), the agent reports all interfaces that are up; T-038 makes the rule for LXC final.
- **When it sends again.** No netlink subscription. The agent reads the list again at every poll round (at most 30 s apart) and sends an `addrs` report when the list differs from the last one the controller took; at most one in 30 s. A read costs one system call. A subscription would be faster by seconds only and adds a socket, a goroutine and a debounce to the agent.
- The agent unit already starts after `network-online.target`, so the first Hello of a healthy guest holds the configured address.

### 3. The Issue rule
- **Own resource `guest:address`,** not `guest:config` (this changes the proposal in "Situation"). Reasons: there is one Issue per Target and resource; the 60 s guest check reports `unchanged` for `guest:config` and would close the Issue; `guest:config` offers revert and accept, and here Plumb can do neither. The Proxmox config is right in this case; the guest does not follow it.
- **Open** when all of these are true: the Target has a static `ip`; its agent is online; the list is known; the address is not in the list; this has been so for the grace time of 2 minutes (counted from `addrs_at`); no Run works on the Target. The change is `{attr: "ip", from: [reported list], to: "<configured address>", risk: "read"}`. Text: "The guest does not use the configured address 192.0.2.41. It reports: 192.0.2.107."
- **Grace.** A booting guest sends its Hello after the network is up, so a healthy guest never has a wrong list for 2 minutes. After a create the Run is still busy with the first apply. The engine checks in the 60 s guest loop, so the Issue comes 2 to 3 minutes after the wrong list.
- **Close.** A list with the address closes the Issue at once (`gone`, by `system`), on the report itself. An offline agent, a stopped guest or an unknown list neither opens nor closes an Issue.
- **No revert, no accept.** `ip` is create-only (D-185). `Resolve` refuses both for `guest:address` (409). Ignore follows D-091 and D-092 with no change: a person or an `apply` token (the risk is `read`); "Ignore once" ends when the reported list changes; "Ignore by rule" lasts.
- **Only the Target's own data.** The check compares the configured address of a Target with the list of the same Target. Plumb draws no conclusion about another Target (no "address conflict" Issue).

### 4. Old agents and old controllers (D-169)
- An agent from before this card sends no `addrs`: the list is unknown, `ips` shows the configured address as today, and no Issue opens. There is no version gate and no update command; a guest gets the new agent when it is made again or re-enrolled.
- `link` refuses unknown fields. A new agent against an older controller would fail its Hello. This can happen only after a controller downgrade, because guests take the agent from their controller; the card notes it and adds no code for it.

### 5. Security
- **Display only.** A reported address is never used to authorize a call and never as a target of a connection. Today the controller opens no connection to a guest at all. A later repair path (SSH) must use the configured address.
- **A guest that lies** (root in the guest can send any list): it can hide its own mismatch or open an Issue on itself. It cannot reach another Target's Issue, status or credential. The UI marks the list "reported by the guest" and escapes it.
- **Limits:** 16 addresses, fixed form, one write per change, one report in 10 s.
- **Tests for this rule:** (a) an agent that reports the address of another Target or of the controller changes nothing for that Target and gets no other right; (b) a source test (same style as the tier matrix test) fails when the stored list is read outside the status view and the address check.

### 6. Web
- Target list and Target screen: the address column shows the reported list with the mark "reported by the guest"; a configured address has no mark. A DHCP Target shows an address for the first time.
- Target screen with a mismatch: "Configured: 192.0.2.41 — the guest reports 192.0.2.107", with a link to the Issue.
- Issue screen for `guest:address`: source text "the agent in the guest", the text of section 3, a hint ("Restart the guest once. A stock Ubuntu 26.04 image takes a static address only from the second boot."), and only the two ignore actions (the screen already follows `issue.actions`).
- Mock and dev mode: one demo Target with a mismatch. `browser-shot` light and dark, Playwright.

### 7. Tests
| Level | What |
|---|---|
| Unit, agent | filter on recorded netlink dumps (temporary, tentative, link-local, loopback, virtual interface, more than 16); "send only on change"; the 30 s limit; 400 on `addrs` stops the retry |
| Unit, controller | list check (every refused form); a bad list in a Hello keeps the link; grace with a set clock; open, update, close; busy Run; offline agent; old agent; DHCP Target; revert and accept refused; ignore once and by rule; the two security tests |
| Slice | static Target with a simulated agent that reports another address: Issue after the grace time, closed by a right list, no Hello count change by an `addrs` report |
| Guest, in CI | Debian 13 and Ubuntu 26.04 containers: Hello holds the address of `eth0`, no loopback, no link-local; `ip addr add` and `ip addr del` give an `addrs` report in one round |
| Real host (once) | stock Ubuntu 26.04 image, static address: first boot → `ips` shows the DHCP address as reported, Issue within 4 minutes; after the first restart → Issue closed, `ips` shows the static address. Debian 13 static: no Issue. A DHCP Target shows its address |

### 8. Child cards (after the user's approval)
| Card | Content | Owner (proposal) | After |
|---|---|---|---|
| T-068 | Contract: `wire` (Hello.addrs, report `addrs`), OpenAPI (`ips_source`, `ips_reported_at`, `guest:address`), mock examples | pl-architect | – |
| T-069 | Agent: address list, filter, Hello, send on change; guest test | pl-backend | T-068 |
| T-070 | Controller: link check and limit, migration, engine check with grace, Issue rule, status view, dev demo | pl-backend (second lane) | T-068 |
| T-071 | Web: address mark, mismatch line, Issue screen for `guest:address` | pl-frontend | T-068 (mock), T-070 (real API) |
| T-072 | QA: tests against T-069 … T-071, the two security tests, the real-host check with pl-backend | pl-codex-qa | all |

T-069, T-070 and the mock part of T-071 run in parallel. No card needs T-039.

### 9. Open questions for the user (each with one recommendation)
1. **Grace time before the Issue opens?** Recommendation: 2 minutes. It is long enough for a slow boot and short enough to see the fault before the first SSH try.
2. **IPv6?** Recommendation: report global and ULA addresses, but no temporary (privacy) addresses.
3. **Addresses of Docker bridges and VPN interfaces in a guest?** Recommendation: do not report them. Plumb shows the addresses of real network cards only.
4. **The agent is offline: which address does Plumb show?** Recommendation: the configured one, as today. An old reported list can be wrong.

## Result (owner, max 8 lines)
branch · commit · tests · notes
