# T-039 – Image builds: Blocks, seal, retention, schedule   (test host ready since 2026-11-11; needs the plan)

- **Owner:** pl-backend   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** L (will be split)   · **Depends on:** test host (D-041), T-012 and the agent PRs (#8, #13) on `main`

## Goal
Plumb builds its own templates from a base image plus Blocks, keeps versions, removes old ones and rebuilds on a schedule.

## Design (decision level – D-098, BRIEF §5.4, ARCHITECTURE §12)
- `Image` spec gets `blocks`, `retention.keep`, `schedule`.
- Build = Run: temporary guest → apply Blocks → agent `seal` → stop → template → image version row.
- A build never changes a target. A target records its image version.
- Blocks of an Image cannot use secret references. `seal` also removes the agent's desired list, so a template holds no secret.
- Retention keeps the newest `keep` versions and every version that a guest still needs.
- A scheduled build is planned by `system`. Its approval rule waits for the user's answer (T-025, question 2).

## Before this card starts
- [ ] `pl-architect` turns the design into a plan with the facts from the host: template conversion, linked and full clones per storage, what `seal` must reset on Debian 13 and Ubuntu 26.04, delete rules for templates with clones.
- [ ] Parts that need no host (schema, version table, retention choice, schedule clock) can become their own unblocked card then.
- [ ] Needs new architecture decisions.
- [ ] Facts from the real host that the plan must cover (pl-lead, 2026-11-11):
  - D-192: `seal` (or a Block) removes initramfs networking on dracut images (Ubuntu 26.04), so a Plumb-built Ubuntu template takes a static address. Proof on the host: static address applied at the first and at a later boot.
  - D-185 / pl-architect: an image build must remove the SSH host keys before the template is made.
  - D-188 (T-055): there is no Image binding; a template is found by name + full SHA-256 in the pool. Templates and import files are never deleted by the driver (D-077): the retention rule of this card decides who deletes them.

## Host questions (pl-architect, 2026-11-11; pl-backend answers them on `pve-dev`)
The plan needs these facts. Write the answers to `docs/tasks/T-039.log.md`: one row per id (`PASS`/`FAIL`/value, time, exact error text). Redact addresses, MACs and token ids. No code change, no installer run.

**Known, do not test again:** the disk storage is `local-lvm` (lvmthin, about 42 GB); the driver always makes a full clone (`full=1`, `driver.go:512`); a clone does not lock its template (T-055 item 3); `seal` exists only in the docs, not in the agent; the D-192 diagnosis (T-015 log).

**Allowed on the host:** read-only commands; test VMs and templates in the pool and the VMID range, made by Plumb or by root `qm`; SSH into test guests with the guest test key (host as jump); root `qm set|shutdown|template|clone|destroy` on these test VMs only, with the guard of T-055 (in range, in the pool, exact name); `pvesm free` for import files of this card by exact name. Not allowed: a new storage, a new right for the token (report the missing right instead), anything from the "never" list of AGENTS.md. End state: 0 guests, 0 templates, 0 import files of this card.

**Method.** Per distribution (Debian 13, Ubuntu 26.04; the pinned images of T-015): Plumb makes build guest `B` (DHCP, 10 GB, Block `nginx`) through the API. Run the seal script below in `B` by SSH. Shut down, clear the config (A2), convert. For the clones use the adoption rule of D-188, so the real driver and the real enrollment run: destroy the stock template, give the sealed template the name `plumb-img-<image>-<sha12>` and the description line `sha256=<full>` of an Image document, then let Plumb make Target `C1` (static, `PLUMB_TEST_VM_IP_1`) and `C2` (DHCP) from it, both with the Block `nginx` and a bootstrap user with a key.

Seal script, candidate (root, in this order; report every step that fails or does not exist):
```sh
systemctl disable --now plumb-agent.service
rm -rf /var/lib/plumb-agent /etc/plumb-agent /usr/local/bin/plumb-agent /usr/local/sbin/plumb-agent-install /etc/systemd/system/plumb-agent.service
# Ubuntu only, D-192 (see D1 … D3)
rm -f /etc/cloud/cloud-init.disabled
cloud-init clean --logs --seed --machine-id --configs all
rm -f /etc/ssh/ssh_host_*
[ -L /var/lib/dbus/machine-id ] || rm -f /var/lib/dbus/machine-id
rm -f /var/lib/systemd/random-seed /var/lib/systemd/credential.secret
userdel -r <bootstrap user of B>; rm -f /etc/sudoers.d/90-cloud-init-users
apt-get clean; journalctl --rotate; journalctl --vacuum-time=1s; fstrim -av
```

### A. Template conversion of a guest that Plumb made
- **A1** With the Plumb token: `POST …/qemu/<B>/template` on the stopped `B` (it ran, it has a cloud-init drive and a grown disk). Works? Time? Volumes after it (`base-…`, cloud-init volume).
- **A2** Print the config of `B` before the conversion (serial redacted). Can the token remove the build values in one write with the digest: `delete=sshkeys,ciuser,ipconfig0,cicustom,nameserver,searchdomain,ciupgrade,onboot`, `smbios1` without the serial, a new `description`? Which key is refused? Can the token change `name` and `description` of a template after the conversion?
- **A3** In a full clone of the template: which values does Proxmox make new (`smbios1` uuid, `vmgenid`, MAC of `net0`) and which does the clone take over? Is any value of `B` left in `C1` after the driver's `configure` (look at `sshkeys` when the Target has no key)?
- **A4** Disk: the template has 10 GB. A Target with `disk: 10` (resize to the same size: error or no-op?), with `disk: 20` (root file system grown at first boot, both distributions?), with `disk: 8` (exact error text of the driver).
- **A5** Space: `lvs` data usage of the template volume and of one full clone, per distribution.

### B. Clones and delete
- **B1** lvmthin: time of a full clone and of a linked clone (`qm clone … --full 0`, root, test VM) of the sealed template. The host has no other disk storage type; say so if that is wrong.
- **B2** Delete of a template: (a) with a full clone alive: allowed? the clone still starts? (b) with a linked clone alive: exact error text. (c) With the Plumb token: `DELETE …/qemu/<template>` works? Which parameters (`purge`, `destroy-unreferenced-disks`)? Are the base disk and the cloud-init volume gone?
- **B3** With the Plumb token: `DELETE …/storage/plumb-images/content/<import volid>`. Works with the rights of today? If not: the exact error and the right that Proxmox names.
- **B4** Read-only: VMID range and free ids of the running controller, free space of `local-lvm`. How many 10 GB templates plus guests fit?

### C. Seal: what it must reset, and the proof (both distributions)
- **C0** Before seal, read-only in `B`: `cloud-init --version`; `cloud-init clean --help` (do `--machine-id`, `--seed`, `--configs` exist?); `ls -l /etc/machine-id /var/lib/dbus/machine-id`; the network files that cloud-init wrote (`/etc/netplan`, `/etc/network/interfaces.d`); what cloud-init left for the bootstrap user (home, `authorized_keys`, sudoers file); `dpkg -l dracut* initramfs-tools`.
- **C1** After the seal script: does `B` shut down clean? Does a clone boot? (A cold boot with `/etc/machine-id` = `uninitialized` and no SSH host keys.)
- **C2** machine-id: three different values in `B` (before seal), `C1`, `C2`; none empty or `uninitialized`. `C2` and a second DHCP clone get different leases.
- **C3** SSH host keys (D-185): fingerprints of `B`, `C1`, `C2` all differ; `sshd` runs; the keys stay after `qm reboot` and after a cold stop and start.
- **C4** cloud-init: `C1` and `C2` ran as a new instance (one directory in `/var/lib/cloud/instances`, instance-id is not the one of `B`), hostname = Target name, the Target's bootstrap user and key exist, the user of `B` does not; after enrollment `cloud-init.disabled` is back.
- **C5** Agent identity: the Run of `C1` succeeded with a new agent; `/var/lib/plumb-agent` holds only the new credential and the new desired list; the agent of `B` is not `online` again. The Block result is in the image: the step `package:nginx` is `unchanged` in the first Run of `C1`, and Nginx answers at first boot.
- **C6** Logs: `journalctl --list-boots` in `C1` shows no boot of `B`; `/var/log/cloud-init.log` starts with this boot; no file under `/var/log`, `/root`, `/home`, `/tmp` holds the hostname, the address or the user of `B` (`grep -rl`).
- **C7** What is left that the script did not reset? Look at `/var/lib/dhcp`, `/var/lib/systemd`, `/etc/hostname`, `/etc/hosts`, `/var/lib/apt/lists`, shell histories. List it; do not fix it.

### D. Ubuntu 26.04: no network setup in the initramfs (D-192)
- **D1** Read-only on a stock Ubuntu guest: `dpkg -l | grep -i dracut`; what needs `dracut-network` (`apt-cache rdepends --installed dracut-network`, `apt-mark showmanual | grep dracut`); `lsinitrd | grep -i -E 'network|dhcp'`; `/proc/cmdline`; which file makes `zzzz-dracut-default.network`.
- **D2** Fix candidate 1, in `B` before seal: `/etc/dracut.conf.d/90-plumb-no-network.conf` with `omit_dracutmodules+=" network systemd-networkd "` (use the module names that D1 shows), then `dracut -f --regenerate-all`. If it does not help, candidate 2: `apt-get -y remove dracut-network` and regenerate. Report which one works and what `apt` wants to remove with it.
- **D3** Proof on `C1` (static): at the **first boot** the configured address answers (neighbor table of the controller LXC, as in T-015), the NIC name and its network file (`networkctl status`), time until the agent is online; the same address after `qm reboot` and after a cold stop and start. `C2` (DHCP) still gets an address; time of its first boot (stock: about 3 min).
- **D4** Does the fix stay after a kernel update in a clone (`apt-get -y install --reinstall linux-image-$(uname -r)` or the next kernel, then `lsinitrd` again)?
- **D5** Debian 13: is the candidate a no-op there (no dracut)? `C1` on Debian takes the static address at first boot as before?

### E. Time and size of a build
- **E1** Seconds per phase, per distribution: `FetchImage` (template there / not there), clone, first boot until the agent is online, apply of `nginx`, seal script, shutdown, conversion. Sum.
- **E2** `apt-get update && DEBIAN_FRONTEND=noninteractive apt-get -y full-upgrade` in a fresh guest from today's stock image: time, number of packages, new kernel yes/no, extra disk use. (The plan decides if a build upgrades the packages, D-186.)
- **E3** (added with the plan draft) A guest with `ciupgrade=1` (the Proxmox default; root `qm set` on a clone of the stock template before its first start, all other values as the driver sets them): does cloud-init finish the package upgrade **before** it runs the vendor `runcmd` (`cloud-init analyze show`: `package_update_upgrade_install` before `scripts_vendor`)? Time of this first boot. After it: the last line of `apt-get -s dist-upgrade` (`0 upgraded, 0 newly installed`? packages kept back by Ubuntu phasing?).

## Plan (pl-architect, 2026-11-11) — DRAFT, the points marked `HOST` wait for the host answers
PLAN ONLY. The user approves it before any card starts, because it changes the agent (`seal`). Proposed decisions: `docs/decisions/T-039.md` (D-197 … D-210).

```
Image document (base url + sha256, build.blocks, retention.keep, schedule)
   │  Plan (origin build | proposal) ── approve ──► Run, one lane per Image
   ▼
FetchImage(base) ─► CreateBuild (clone, DHCP, ciupgrade=1, build token) ─► agent enrolls in build mode
 ─► apply Blocks ─► seal ─► stop ─► MakeImage (clear config, rename, convert) ─► image_version = ready
 ─► retention (remove old versions, old base template, old import file)
Target create: newest ready version of its Image (found by name + build digest, D-188) ─► full clone
```

### 1. Contract and data model
- **Image spec (schema `internal/state/schema/image.json`).** New optional parts; an Image without `build` works as today (stock image).
  ```yaml
  spec:
    type: vm
    url: https://…            # the base image, as today
    sha256: …
    build:                    # present = Plumb builds templates from the base
      blocks: [{use: nginx}]  # same shape as Target.blocks; no secret reference, no secret parameter
      upgrade: true           # default true: all packages are upgraded in the build (D-186)
      guest: {cores: 1, memory: 1024, disk: 10, bridge: vmbr0, ip: dhcp}   # the build guest; disk = smallest disk of a Target
    retention: {keep: 3}      # only with build; 1 … 20, default 3
    schedule: {every: week, weekday: sun, at: "03:00"}   # only with build; every: day | week | month (day: 1 … 28); UTC
  ```
- **Checks when a document is saved** (`state.Validate`): Blocks of an Image exist and render; a `{secret: …}` value or a `writeOnly` parameter is an error; `Target.guest.disk` ≥ `build.guest.disk` of its Image (`HOST A4`); `retention` and `schedule` need `build`.
- **Recipe.** `state.RenderImage` gives the flat resource list of an Image. The recipe is `{type, url, sha256, upgrade, guest.disk, resources}`; its SHA-256 says if two revisions build the same thing.
- **Image version (migration 0008).** Table `image_version(image, version, status building|ready|failed|removed, trigger change|manual|schedule, run_id, revision_id, recipe, base_sha256, digest, disk_gb, started_at, finished_at, removed_at)`, key `(image, version)`. Numbers count up per Image name and are never used again, also after an Image delete. Table `image_schedule(image, last_slot)`. `plan.origin` gets `build`; the Plan JSON gets `images[]`.
- **No binding (D-188).** The row is the record of a build. It holds no VMID. The template is `plumb-img-<name>-v<N>`; its description holds `plumb-image: <name>`, `version=<N>` and `build=<digest>` (SHA-256 over image name, version, recipe, base checksum and Run id). The driver finds a version by name plus the full digest line, in the pool and the VMID range. A row whose template is gone becomes `removed` when a create or the retention sees it.
- **A Target and its version.** A create takes the newest `ready` version whose template exists. The driver writes `plumb-image-version: <N>` next to `plumb-image: <name>` (D-189) and `Observe` reads it; it is information only and no part of `Diff`. A new version changes no Target.
- **API** (`api/openapi.yaml`, new operations with `x-plumb-planned`): `ImageView.status` = `{versions[], building, last_build, next_build_at}`; `POST /images/{name}/build` → Plan (tier `plan`); `Plan.images[]` = `{image, build: {version, trigger, upgrade, resources[]}, removes[]}`; `Plan.human_only` gets `image-recipe`; `Run.images[]` with steps; `TargetStatus.image_version`. Events with the subject `image:<name>`.

### 2. Build = Run
- **Triggers.** (a) `change`: a Plan whose proposal makes or changes the recipe of a built Image builds it in its Run. (b) `manual`: `POST /images/{name}/build`. (c) `schedule`: the controller plans as `system` (section 6). (b) and (c) are Plans of origin `build` with no document change.
- **No new revision for origin `build`.** Today every approval commits a revision, so a weekly build would make every pending Plan stale. An approved `build` Plan commits nothing; its Run uses the head revision.
- **Steps** (rows in `run_step`, target `image:<name>`): `image:base` (FetchImage) → `guest:config` (CreateBuild) → wait for the agent → one step per resource (the normal `apply`) → `image:seal` → `image:template` (stop, MakeImage) → `image:retention`.
- **Build guest.** VM `plumb-build-<name>-v<N>`, description `plumb-build: <name> v<N>`, `onboot=0`, user `plumb-build` without a key, `ciupgrade=1` when `upgrade` is true (`HOST E3`). Its link name is `build:<name>`; a Target name cannot hold `:`. The enrollment token is marked `build`. The controller sends no `watch` and drops drift from `build:` agents.
- **One build per Image at a time.** A second build Plan for the same Image is refused at approval ("build running").
- **Failure and crash.** A failed step deletes the build guest, sets the version `failed` and fails the Run; older versions stay. After a controller restart `Recover` fails the Run as today and removes every guest with a `plumb-build:` marker. The next build of the Image does the same first.
- **Limits in one Plan.** A Plan cannot create a Target from an Image that the same Plan builds, and a Target of a built Image with no `ready` version is refused at propose time with "build the Image first". (Later: one Run that builds first.)
- **Timeouts.** Enroll 20 min (first boot plus upgrade), apply 15 min, seal 10 min. `HOST E1` sets the final values.

### 3. Risk classes and approval (D-098, D-099, D-101)
- A build has the risk `reversible`: it makes a new template and changes no guest. Retention inside a build removes only what the approved `keep` allows; it is listed in the Plan (`removes`).
- In the build guest every resource is a `create`; the `apply` goes out with no risk limit, because the guest has no state to lose.
- **New human-only reason `image-recipe`:** only a human session approves a Plan that changes the recipe, the `schedule` or the `retention` of a built Image, makes one or deletes one. `plan` computes it by content (recipe of the base revision against the proposal) for every origin, like D-099. So a Block change that reaches a built Image is human-only too. Reason: a template is the start of every later guest, a Target Plan does not show what is inside it, and a schedule runs with no click.
- A `manual` build has an unchanged recipe: a human or an `apply` token (any `max_risk`) approves it.
- `system` approves only inside the engine, with no HTTP path, and only a Plan with: origin `build`, trigger `schedule`, no document change, no `human_only` reason, a slot that is due.
- A stock Image (no `build`) keeps today's rules.

### 4. Agent: command `seal` and its security
- **Wire.** `CommandSeal = "seal"`, body `{plan_id, upgraded}`. The body holds no path, no command and no user name: what `seal` does is fixed in the agent. The answer is one `result` with one line per step.
- **Steps, in this order** (final list after `HOST C0, C7, D2`): (1) the guards below; (2) Ubuntu, D-192: when dracut is the initramfs tool and its network part is present, write `/etc/dracut.conf.d/90-plumb-no-network.conf` and make the initramfs again; (3) when `upgraded`: `apt-get -s dist-upgrade` must say `0 upgraded, 0 newly installed`, else fail with the end of the cloud-init output log; (4) cloud-init: remove `/etc/cloud/cloud-init.disabled`, `cloud-init clean --logs --seed --machine-id --configs all`; (5) remove `/etc/ssh/ssh_host_*` (D-185); (6) machine-id (also the dbus file when it is no link); (7) remove the user `plumb-build`, its home and its sudoers file; (8) systemd random seed and `credential.secret`; (9) `apt-get clean`, journal, shell histories, `/tmp`, `/var/tmp`; (10) `fstrim`; (11) send the result; when the controller took it: remove the credential, the desired list, the unit, the config, the CA file, the install script and the binary, then stop.
- **The agent is not in a VM template.** A clone installs the current agent from the controller through the vendor snippet, as with a stock image. So no template holds an old agent, a credential or a desired list. (LXC templates need the agent inside; T-038 adds that variant.)
- **Guards: two independent ones, so one defect cannot seal a real Target.** Controller: `link.Send` takes `seal` only for a link name with the prefix `build:`, and the engine sends it only from a build lane. Agent: it runs `seal` only when its enrollment answer said `build` (stored as a file in the state directory) **and** it never got a `watch`; else it answers `refused` and changes nothing. An enrollment token is marked `build` only by `CreateBuild`.
- After the result the controller revokes the agent with the revoke function of T-057 (D-211; this plan adds no second one) before it stops the guest. A failed `seal` step fails the build; the guest is deleted, never converted.
- **What stays the same:** the trust model (§7). The controller already has root in every guest; `seal` adds no new power to a token, and it gets no input from a document.

### 5. Driver (`internal/driver`), retention and delete
- **New methods:** `CreateBuild(BuildSpec, Bootstrap)`, `MakeImage(GuestID, BuiltImage) ImageRef` (guard: build marker, stopped; clears `sshkeys, ciuser, ipconfig0, cicustom, nameserver, searchdomain, ciupgrade`, the serial; sets name and description; converts — `HOST A1 … A3`), `FindImage(name, version, digest)`, `ListImages(name)`, `RemoveImage(object)`. `GuestSpec.ImageVersion` for the marker. `driver/fake` models versions.
- **Full clones stay** (`HOST B1`). So no guest needs a template after its create, and "every version that a guest still needs" (D-098) has no case. `RemoveImage` still fails safe when Proxmox refuses (`HOST B2`).
- **Retention.** Keep the newest `keep` versions with the state `ready`. Remove older `ready` versions, every `failed` rest, base templates and import files of this Image with another checksum than the document. It runs as the last step of a build, and in the Run of a Plan that lowers `keep`, changes the base checksum or deletes the Image (then everything of that name goes). A Target that names the Image still blocks the delete of the document.
- **Who deletes (D-077, D-188).** Only `RemoveImage`, only from the retention step of an approved Run. `Driver.Delete` still never removes a template. Guard before each delete: in the VMID range, in the pool, `template: 1`, no lock, the exact name (`^plumb-img-<name>-(v[0-9]+|[0-9a-f]{12})$`), and the description names this Image with the full digest or checksum. An object that fails the guard is reported and left.
- **Rights.** `HOST B2c, B3`: if the token cannot delete an import file, the plan has two ways: a new right through the installer (changes D-081, a user question), or the file stays and the Image screen shows the `pvesm free` line.

### 6. Schedule
- `plan.Engine.RunSchedules(ctx)` ticks every minute (same pattern as `PollGuests`). `plan.Config.Now` is new, so tests set the clock.
- A slot is due when its time is past and it is newer than `image_schedule.last_slot`. After a controller stop the engine runs the newest missed slot once, not all of them. A new or changed schedule starts with the next slot.
- Scheduled builds run one after the other. A slot is skipped when a build of the Image runs.
- A failed scheduled build is not tried again before the next slot. The Image shows "last build failed" with the Run; "Build now" is the retry.

### 7. Web
- New screens: **Images** (list: name, base, built or stock, newest version, last build, next build), **Image** (versions with state, date, Run link and the Targets made from each; recipe; "Build now"), **Image form** (base, build on/off, Blocks without secret parameters, upgrade, build guest, keep, schedule). Routes `/images`, `/images/new`, `/images/:name`, `/images/:name/edit`.
- Changes: the Plan screen shows an Image part (new version, resources, "removes v1", the label "A person must approve: image recipe"); the Run screen shows the build steps; the Target screen shows "Image ubuntu-26.04 v4"; the Target form checks the smallest disk.
- Mock first (`web/src/lib/mock/mock.ts`), then the real API; `browser-shot` in light and dark; Playwright on `plumb serve --dev` with the fake driver and a simulated `seal`.

### 8. Tests
| Level | What |
|---|---|
| Unit | schema and save checks (secret refused, disk rule), recipe hash, `image-recipe` for every origin, due slots with a set clock, retention choice, delete guard, `link.Send` refuses `seal` for a Target |
| Slice (`driver/fake` + `linktest`) | build → version → Target from it; fourth build with `keep: 3` removes v1; failed seal leaves no guest and no version; crash in each step, then `Recover`; scheduled build approved by `system` and no stale Plan |
| Agent unit | every seal step on a temp root with the fake runner; both guards; "result first, then remove yourself" |
| Guest, in CI | `seal` as root in Debian 13 and Ubuntu 26.04 containers: the test image gets `cloud-init` and `openssh-server`; after seal no host key, machine-id reset, no cloud-init state, no agent file, unit gone; a normal agent refuses `seal`. The initramfs step cannot run in a container: fake runner only |
| Proxmox replay | recorded answers for `CreateBuild`, `MakeImage`, `FindImage`, `RemoveImage`, with the leak scan |
| Real host (`make test-pve`, not CI) | per distribution: build, two Targets from the version; the proofs C2 … C6; Ubuntu static address at the first boot, after a reboot and after a cold start (D-192); retention removes a version with the token; a short schedule; search of the template disk for the build credential and the token |

### 9. Child cards (after the user's approval)
| Card | Content | Owner (proposal) | After |
|---|---|---|---|
| T-058 | Contract: Image schema, OpenAPI, `wire` seal, driver interface with fake stubs, mock examples | pl-architect | – |
| T-059 | State and store: save checks, `RenderImage`, recipe, migration 0008 | pl-backend | T-058 |
| T-060 | Agent `seal`, guards, build mode at enrollment, guest tests | pl-backend (second lane) | T-058 |
| T-061 | Driver: `CreateBuild`, `MakeImage`, `FindImage`, `ListImages`, `RemoveImage`, version marker, recordings from the host | pl-backend | T-058 |
| T-062 | Engine: build lane, `Plan.images`, `image-recipe`, Target from a version, retention step, crash cleanup, API | pl-backend | T-059, T-061 |
| T-063 | Schedule: clock, slots, `system` approval | pl-backend | T-062 |
| T-064 | Web: Images screens, form, Plan/Run/Target changes | pl-frontend | T-058 (mock), T-062 (real API) |
| T-065 | QA: tests against the cards above, mutation audit, guest tests | pl-codex-qa | each card |
| T-066 | Real-host acceptance, both distributions; then remove the known limit D-192 from ARCHITECTURE §5 | pl-backend + pl-codex-qa | all |

T-059, T-060, T-061 and the mock part of T-064 run in parallel.

### 10. Open questions for the user (each with one recommendation)
1. **Who approves a change of an Image recipe, schedule or retention?** Recommendation: only a person (`image-recipe`). A token can still start a build of an approved recipe.
2. **Form of the schedule?** Recommendation: the simple form (day, week or month, plus a time in UTC), no cron text. The UI shows the next build in local time.
3. **Does a build upgrade all packages?** Recommendation: yes by default (`upgrade: true`), so a scheduled build gives a fresh template (D-186).
4. **A scheduled build fails. What then?** Recommendation: no second try before the next slot; the Image shows the failure and "Build now".
5. **How many versions stay?** Recommendation: 3 by default (`HOST A5` gives the disk space per version).
6. **Only if `HOST B3` says no:** may the installer give the token the right to delete import files? Recommendation after the host answer.

### Not in this plan
A hand delete of one version; a list of old templates of Images that no longer exist; one Run that builds an Image and then creates its Targets; LXC images (T-038); a notice outside the UI for a failed build.

## Out of scope
- Signed image checksums. Images for other distributions.

## Result (owner, max 8 lines)
branch · commit · tests · notes
