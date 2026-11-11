# T-053 – `plumb serve` uses the Proxmox driver

- **Owner:** tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M (Claude; ~300 lines with tests)   · **Depends on:** T-012 on `main` (PR #20); the real-host part also needs T-013 (PR #26)
- **State:** plan ready; code starts when PR #20 is merged (or on a branch on top of it)

## Goal
`plumb serve` without `--dev` builds the Proxmox driver from the `PLUMB_PVE_*` settings of the installer, checks the host at the start and says clearly what is wrong. After this card the install on the test host gives a controller that runs and creates a real VM.

## Context (read only these)
- `cmd/plumb/serve.go` – `runServer`, the `default:` branch of the driver switch (l. 156–162); `cmd/plumb/serve_test.go` l. 51
- `internal/driver/proxmox/client.go` (`ClientConfig`, `NewClient`, `APIError`), `driver.go` (`Config`, `New`, `members`, `checkSpec`), `pvefake_test.go` – from PR #20
- `install/install.sh` on `origin/pl-backend/T-013-minimal-installer`: `push_env`, `UNIT`, `fetch_plumb_ca`, the end of `do_install`
- `docs/DECISIONS.md` D-039, D-072, D-076 … D-084, D-086; `docs/decisions/T-053.md`

## Acceptance criteria
- [ ] Without `--dev`, `serve` reads the settings of the Plan (table), builds `proxmox.Client` and `proxmox.Driver`, and runs the start-up check before it touches the data directory.
- [ ] Each failure of the Plan (error table) stops the start with exit 1 and its text. A missing or bad setting names every wrong variable in one message. No text, log line or event holds `PLUMB_PVE_SECRET`.
- [ ] A failed start creates no file in `--data`, prints no admin password and opens no listener.
- [ ] A good start logs one line: URL, node, Proxmox VE version, pool, VMID range (no secret). Then all is as today: admin password once, CA in `<data>/link/ca.crt`, API on 8443, agents on 8444.
- [ ] `--dev` is unchanged (D-039): fake driver, `--dev --driver proxmox` is exit 2, and `--dev` sends no request to `PLUMB_PVE_URL` even when the variables are set.
- [ ] The driver refuses a guest on a bridge other than `PLUMB_PVE_BRIDGE` before any Proxmox call (D-184).
- [ ] Tests: `make lint test build` passes. New tests as in the Plan.
- [ ] Real host: the list "Acceptance on the real host" passes; the log is `docs/tasks/T-053.log.md` (no address, token, password or key in it).

## Out of scope
- Changes to `install/install.sh` (report a defect to `pl-lead`). Reload of the settings without a restart. A UI page for the host state.
- A degraded mode (UI up while Proxmox is down). A check of the bridge at propose time.
- New Proxmox rights for the token: D-081 stays as it is.

## Plan (architect)

### Settings (D-182)
`serve` reads them from the environment only (the unit has `EnvironmentFile=/etc/plumb/plumb.env`). No flag carries them, so the secret is never in `argv`. New file `cmd/plumb/pve.go`: `pveSettings(getenv) (proxmox.ClientConfig, proxmox.Config, error)`.

| Variable (written by `push_env`) | Goes to | Rule |
|---|---|---|
| `PLUMB_PVE_URL` | `ClientConfig.URL` | required, `https://host[:port]` |
| `PLUMB_PVE_TOKEN_ID` | `ClientConfig.TokenID` | required, `user@realm!name` |
| `PLUMB_PVE_SECRET` | `ClientConfig.Secret` | required; never printed |
| `PLUMB_PVE_CA` | `ClientConfig.CAFile` | optional; empty = system roots (D-076) |
| `PLUMB_PVE_NODE`, `_POOL`, `_DISK`, `_IMAGES`, `_SNIPPETS`, `_SNIPPET` | `Config.Node`, `Pool`, `DiskStorage`, `ImageStorage`, `SnippetStorage`, `Snippet` | required |
| `PLUMB_PVE_BRIDGE` | `Config.Bridge` (new field) | required (D-184) |
| `PLUMB_PVE_VMID_RANGE` | `Config.VMIDMin`, `VMIDMax` | required, `<min>-<max>`, min ≥ 100 |
| `PLUMB_HOSTS` | – | not read by `serve`; the unit expands it into `--hosts` |

### Start-up check (D-183)
New `internal/driver/proxmox/check.go`: `func (d *Driver) Check(ctx) (version string, err error)`. It uses only rights that the token has. `runServer` calls it in the driver switch, after the key file check and before `MkdirAll(f.data)`.
1. `GET /version` → proves TLS, the token and the version (≥ 9.1).
2. `GET /pools?poolid=<pool>` (the call of `members`) → the answer must hold the pool.
3. `GET /nodes/<node>/storage` → the disk storage has content `images`, the image storage `import`, the snippet storage `snippets`; each is enabled and active.
- Steps 2 and 3 report all their problems in one error.
- A host that does not answer (dial error, timeout, 5xx) is tried again every 5 s for 60 s (the LXC can start before `pveproxy`). Then exit 1; systemd starts `serve` again.
- **No check of the snippet file.** The installer writes it after the first start, because it needs the Plumb CA (`fetch_plumb_ca` → `write_snippet`). A start that needs the snippet never ends the install.
- The package floors of D-081 (`qemu-server`, `libpve-storage-perl`) need `Sys.Audit`; only the installer checks them.

### Error texts (after `plumb serve: `)
| Case | Text |
|---|---|
| variables missing | `the Proxmox settings are missing: <names>. install.sh writes them to /etc/plumb/plumb.env; use --dev to try Plumb without Proxmox` |
| bad value | `<NAME> must be <form>` (the value is shown, except for the secret) |
| CA file | `cannot read the Proxmox CA file <path>: <error>` |
| wrong CA or name | `the certificate of <url> is not signed by the CA in <path>, or it does not name this host. Copy /etc/pve/pve-root-ca.pem of the node to <path>` |
| bad token (401) | `Proxmox refused the API token <token id>. The secret in the settings is wrong or the token was removed` |
| no answer | `cannot reach Proxmox at <url>: <error>` |
| too old | `Plumb needs Proxmox VE 9.1 or newer; this host has <version>` |
| pool | `pool <pool> does not exist, or the token has no Pool.Audit right on /pool/<pool>` |
| node | `node <node> does not exist on this host` |
| storage | `storage <id> is missing on node <node>, is not active, or the token has no right on it` / `storage <id> does not have the content "<content>"` |

`Check` returns typed errors (`errors.As` on the x509 and TLS errors, `APIError.Status`); the texts are made in `cmd/plumb`.

### What stays for `--dev`
Everything (D-039, D-072): fake driver, plain HTTP, simulated agents, seed data, the `mode` marker. `--dev` never calls `pveSettings`. The default driver without `--dev` stays `proxmox`.

### Tests (fake PVE, `make test`)
- `internal/driver/proxmox`: `Check` against `pvefake_test.go` (add `/version` and `/nodes/<node>/storage`): good host; 401; version 8.4 and 9.0; pool not in the answer; each storage missing, inactive, wrong content; two problems in one error; 503 then 200 (short retry interval in the test); bridge refusal in `Create` and `Update` with zero requests.
- `cmd/plumb`: `pveSettings` table test (each variable missing, bad range, bad URL, no CA). One start against an `httptest.NewTLSServer` with the three routes and its certificate as `PLUMB_PVE_CA`: the start reaches "Plumb listens on". The same server with another CA file: the wrong-CA text. Failed start: `--data` stays empty and the output has no "Password". A canary secret is in no output. `--dev` with the variables set: zero requests.
- Change `serve_test.go` l. 51 to the "settings are missing" text.

### To verify on host (assumptions of the check)
1. `GET /version` works with the privilege-separated token and no extra right; a wrong secret and a removed token both answer 401.
2. `GET /nodes/<node>/storage` lists all three storages with `content`, `enabled`, `active` under D-081 (the snippet storage has only `Datastore.AllocateSpace`). If not: use `GET /nodes/<node>/storage/<id>/status` for each storage. If that also fails, drop this storage from the check; do not add a right.
3. The answer for a wrong node name.

### Acceptance on the real host (`pve-dev`; the agent that `pl-lead` names; AGENTS.md "Test host")
The host has the install of T-013 run 3 (LXC, key, `plumb.env`). So this is a **repair run**: no `plumb key init`, no TTY needed.
1. `make build`; `install.sh --plumb-binary bin/plumb --dry-run`; send the result to `pl-lead`; wait for the go; then the real run.
2. `plumb.service` is active and does not restart. The journal has the Proxmox line and "Plumb listens on".
3. The journal has the "First start. User: admin  Password:" line exactly once; a restart of the service adds no second line.
4. The snippet on the host holds the CA of `/var/lib/plumb/link/ca.crt` and `https://<LXC address>:8444` (mode 0644, root). It replaces the throwaway CA of T-013 step 5.
5. `https://<LXC address>:8443` answers with a certificate of the Plumb CA (`curl --cacert`; `browser-shot` if `mobtest` has a route). Log in as `admin`, change the password. Put the new password and one API token into 0600 files in `~/.config/plump/`.
6. Negative starts in the LXC as user `plumb`, each a one-off `plumb serve --data <new temp dir>` with one changed variable: wrong secret, wrong CA file, wrong pool, wrong image storage. Each gives its text and leaves the temp directory empty. Do not change `plumb.env` or the service.
7. First VM: an Image document (Debian 13 cloud image, pinned SHA-256) and a Target with `deploy-user` → Plan → approve. The Target goes `provisioning` → `enrolling` → `ready`, agent `online` (the contract has no state named "enrolled").
8. End: delete the Target through a Plan. Remove the image template as root with `qm destroy <vmid>` (the driver never deletes a template, D-077). The LXC and the install stay for T-015.

This also closes the open T-013 items "repair run", "UI + first login" and "agent unit starts in a fresh VM".

### What the real-host part of T-015 needs after this card
- This card merged, and the LXC on the test host with that build (step 8 keeps it).
- In `~/.config/plump/testhost.env` (never in the repo): base URL, path of the Plumb CA file (a copy of `link/ca.crt`), path of the API token file. `test/pve/slice_pve_test.go` reads them and skips without them.
- Image documents for Debian 13 and Ubuntu 26.04 with pinned SHA-256. The host needs internet access (`download-url`).
- Static guest addresses: `Observe` gives no address for a DHCP guest (D-079), and the test must change a file in the guest over SSH. `pl-lead` names two free addresses and the gateway; the Targets set `ip`, `gateway`, `user`, `ssh_keys` (a test key).
- Root actions on the host that the T-015 card must list: `qm set <vmid> --memory …` (the `guest:config` drift), `qm reboot <vmid>`, `qm destroy` for the templates at the end.
- Time limits: agent drift opens an Issue within 10 s; `guest:config` drift comes from `PollGuests` (60 s), so that limit is ~90 s.
- `make test-pve` gets `./test/pve/...` as a second step. The driver tests and the slice test share pool and VMID range (9000–9099): never run both at once.

## Result (owner, max 8 lines)
branch · commit · tests · notes
