# T-053 – `plumb serve` uses the Proxmox driver

- **Owner:** tbd   · **Reviewer:** pl-reviewer   · **QA:** pl-codex-qa
- **Size:** M (Claude; ~350 lines with tests)   · **Depends on:** T-012 on `main` (PR #20); the real-host part also needs T-013 (PR #26)
- **State:** plan ready and reviewed (PR #86); code starts when PR #20 is merged (or on a branch on top of it)

## Goal
`plumb serve` without `--dev` builds the Proxmox driver from the `PLUMB_PVE_*` settings of the installer, checks the host at the start and says clearly what is wrong. After this card the install on the test host gives a controller that runs and creates a real VM.

## Context (read only these)
- `cmd/plumb/serve.go` – `serve`, `runServer`, the `default:` branch of the driver switch (l. 156–162); `cmd/plumb/serve_test.go` l. 51
- `internal/driver/proxmox/client.go` (`ClientConfig`, `NewClient`, `APIError`), `driver.go` (`Config`, `New`, `members`, `checkSpec`, `Update`), `pvefake_test.go` – from PR #20
- `install/install.sh` on `origin/pl-backend/T-013-minimal-installer`: `push_env`, `UNIT`, `check_storages`, `fetch_plumb_ca`, the end of `do_install`
- `docs/DECISIONS.md` D-039, D-072, D-076 … D-084, D-086; `docs/decisions/T-053.md`

## Acceptance criteria
- [ ] Without `--dev`, `serve` reads the settings of the Plan (table), builds `proxmox.Client` and `proxmox.Driver`, and runs the start-up check before it touches the data directory.
- [ ] Each failure of the Plan (error table) stops the start with its text and its exit code: 78 for a failure that a restart cannot repair, 1 for "no answer". A missing or bad setting names every wrong variable in one message. No text, log line or event holds `PLUMB_PVE_SECRET`.
- [ ] A failed start creates no file in `--data`, prints no admin password and opens no listener.
- [ ] A good start logs one line: URL, node, Proxmox VE version, pool, VMID range (no secret). Then all is as today: admin password once, CA in `<data>/link/ca.crt`, API on 8443, agents on 8444.
- [ ] `--dev` is unchanged (D-039): fake driver, `--dev --driver proxmox` is exit 2, and `--dev` sends no request to `PLUMB_PVE_URL` even when the variables are set.
- [ ] The driver refuses a guest on a bridge other than `PLUMB_PVE_BRIDGE` before any Proxmox call (D-184).
- [ ] Tests: `make lint test build` passes. New tests as in the Plan. No test can reach a real host.
- [ ] Real host: the list "Acceptance on the real host" passes; the log is `docs/tasks/T-053.log.md` (no address, token, password or key in it).

## Notes from the T-012 review (pl-lead, 2026-11-11)
- D-187: without the two DNS settings `serve` must stop at the start and name them (same rule as every required setting). The real-host steps need an `install.sh` that writes them (PR #26).
- The start-up check must see the raw transport error (`*url.Error`, x509) for its error table: do not pass it through the driver's `scrub()` first, because a scrubbed error loses the type for `errors.As`.

## Out of scope
- Changes to `install/install.sh` (report a defect to `pl-lead`). Reload of the settings without a restart. A UI page for the host state.
- A degraded mode (UI up while Proxmox is down). A check of the bridge at propose time.
- New Proxmox rights for the token: D-081 stays as it is.
- An exit code other than 1 for failures that are not about Proxmox (key file, data directory).

## Plan (architect)

### Settings (D-182)
`serve` reads them from the environment only (the unit has `EnvironmentFile=/etc/plumb/plumb.env`). No flag carries them, so the secret is never in `argv`. New file `cmd/plumb/pve.go`: `pveSettings(getenv) (proxmox.ClientConfig, proxmox.Config, error)`.

| Variable (written by `push_env`) | Goes to | Rule |
|---|---|---|
| `PLUMB_PVE_URL` | `ClientConfig.URL` | required, `https://host[:port]` |
| `PLUMB_PVE_TOKEN_ID` | `ClientConfig.TokenID` | required, `user@realm!name`; no white space, no `=` |
| `PLUMB_PVE_SECRET` | `ClientConfig.Secret` | required; no white space, no `=`; never printed |
| `PLUMB_PVE_CA` | `ClientConfig.CAFile` | optional; empty = system roots (D-076) |
| `PLUMB_PVE_NODE`, `_POOL`, `_DISK`, `_IMAGES`, `_SNIPPETS`, `_SNIPPET` | `Config.Node`, `Pool`, `DiskStorage`, `ImageStorage`, `SnippetStorage`, `Snippet` | required |
| `PLUMB_PVE_BRIDGE` | `Config.Bridge` (new field) | required (D-184) |
| `PLUMB_PVE_VMID_RANGE` | `Config.VMIDMin`, `VMIDMax` | required, `<min>-<max>`, min ≥ 100 |
| `PLUMB_PVE_NAMESERVER`, `PLUMB_PVE_SEARCHDOMAIN` | `Config.Nameservers`, `SearchDomain` (PR #20) | required (D-187, added by pl-lead after the T-012 review); the installer writes them from the host DNS |
| `PLUMB_HOSTS` | – | not read by `serve`; the unit expands it into `--hosts` |

- After `pveSettings`, `serve` runs `os.Unsetenv("PLUMB_PVE_SECRET")`: a later child process does not get it. (`/proc/<pid>/environ` still shows the start value; the file mode 0600 and the user `plumb` protect that.)
- `ClientConfig` and `Client` have a redacting `String` and `LogValue` already (PR #20). No error path formats `Secret`; the canary test proves it.
- Bridge (D-184): `checkSpec` refuses another bridge. In `Update` the check comes first, before `own()` and `config()`.

### Start-up check (D-183)
New `internal/driver/proxmox/check.go`: `func (d *Driver) Check(ctx) (version string, err error)`. It uses only rights that the token has. `runServer` calls it in the driver switch, after the key file check and before `MkdirAll(f.data)`.
1. `GET /version` → proves TLS, the token and the version (≥ 9.1). The compare is numeric (`9.10` > `9.9`, `10.0` > `9.1`). A version that cannot be parsed fails closed with its own text.
2. `GET /pools?poolid=<pool>` (the call of `members`) → the answer must hold the pool.
3. `GET /nodes/<node>/storage` → each of the three storages is enabled and active, and its `content` list obeys the rules of D-081 (as `check_storages` in `install.sh`): the disk storage has `images` and does not have `snippets`; the image storage is exactly `import`; the snippet storage is exactly `snippets`. An admin can change a storage after the install.
4. Optional (host item 4): `GET /access/permissions` → the token has `SDN.Use` on `/sdn/zones/localnetwork/<bridge>`.
- Steps 2 … 4 report all their problems in one error.
- **Retry: only step 1, and only for a dial error, a timeout or a 5xx/595 answer.** Every 5 s for 60 s (the LXC can start before `pveproxy`). An x509 or TLS error and a 401 are never tried again. Steps 2 … 4 are never tried again; a 5xx/595 on `/nodes/<node>/storage` is the node text, not "cannot reach". The loop ends at once when `ctx` ends (SIGTERM).
- **Exit codes:** "no answer" is exit 1; systemd starts `serve` again (`Restart=on-failure`). Every other failure of the table is exit 78 (`EX_CONFIG`): a typed error in `cmd/plumb` (`errConfig`), mapped in `serve()`. Without a unit rule, a 401 would give one log line and one request every 5 s with no end. **Follow-up for `pl-lead` (not this card):** `RestartPreventExitStatus=78` in the `UNIT` of `install.sh`.
- **No check of the snippet file.** The installer writes it after the first start, because it needs the Plumb CA (`fetch_plumb_ca` → `write_snippet`). A start that needs the snippet never ends the install.
- The package floors of D-081 (`qemu-server`, `libpve-storage-perl`) need `Sys.Audit`; only the installer checks them.

### Error texts (after `plumb serve: `)
| Case | Exit | Text |
|---|---|---|
| variables missing | 78 | `the Proxmox settings are missing: <names>. install.sh writes them to /etc/plumb/plumb.env; use --dev to try Plumb without Proxmox` |
| bad value | 78 | `<NAME> must be <form>, not <value>` – the value with `%q`, cut to 100 characters; never for the secret |
| CA file | 78 | `cannot read the Proxmox CA file <path>: <error>` |
| unknown issuer | 78 | `the certificate of <url> is from "<issuer>"; the CA in <path> did not sign it. If the node uses its own CA, copy /etc/pve/pve-root-ca.pem of the node to <path>; for another certificate, put its CA there` |
| wrong name | 78 | `the certificate of <url> does not name <host>; it names <SANs>. Set PLUMB_PVE_URL to one of these names` |
| expired, not yet valid | 78 | `the certificate of <url> is not valid at this time (<not before> … <not after>); check the clock of the LXC and the certificate of the node` |
| bad token (401) | 78 | `Proxmox refused the API token <token id>. The secret in the settings is wrong or the token was removed` |
| no answer | 1 | `cannot reach Proxmox at <url>: <error>` |
| too old | 78 | `Plumb needs Proxmox VE 9.1 or newer; this host has <version>` |
| version not readable | 78 | `cannot read the Proxmox VE version from <value>` |
| pool | 78 | `pool <pool> does not exist, or the token has no Pool.Audit right on /pool/<pool>` |
| node | 78 | `node <node> does not exist on this host` |
| storage | 78 | `storage <id> is missing on node <node>, is not active, or the token has no right on it` / `storage <id> has the content "<list>"; it must have <rule> (D-081)` |
| bridge right | 78 | `the token has no SDN.Use right on bridge <bridge>` |

`Check` returns typed errors (`errors.As` on `x509.UnknownAuthorityError`, `x509.HostnameError`, `x509.CertificateInvalidError`, and `APIError.Status`); the texts are made in `cmd/plumb`.

### What stays for `--dev`
Everything (D-039, D-072): fake driver, plain HTTP, simulated agents, seed data, the `mode` marker. `--dev` never calls `pveSettings`. The default driver without `--dev` stays `proxmox`.

### Tests (fake PVE, `make test`)
- **No test reaches a real host.** The dev VM has test-host settings. Every `cmd/plumb` test that runs `serve` without `--dev` sets all `PLUMB_PVE_*` with `t.Setenv` (empty or fake values); one helper does it.
- `internal/driver/proxmox`: `Check` against `pvefake_test.go` (add `/version`, `/nodes/<node>/storage`, `/access/permissions`): good host; 401 (one request, no retry); versions 8.4, 9.0, 9.9, 9.10, 10.0, `x`; pool not in the answer; each storage missing, inactive, wrong content; disk storage with `snippets`; image storage with `import,iso`; two problems in one error; 503 then 200 on `/version` (short interval in the test); 595 on the storage call → node error, one request; `ctx` cancel during the retry returns at once; bridge refusal in `Create` and `Update` with zero requests.
- `cmd/plumb`: `pveSettings` table test (each variable missing, bad range, bad URL, white space in the token id, no CA, a long value with control characters). One start against an `httptest.NewTLSServer` with the routes and its certificate as `PLUMB_PVE_CA`: flags `--data <tmp>`, `--key-file <tmp key>` (made with `secret.WriteKeyFile`), `--listen 127.0.0.1:0`, `--agent-listen 127.0.0.1:0`; the start reaches "Plumb listens on". The same server with another CA file: the unknown-issuer text, exit 78. Failed start: `--data` stays empty and the output has no "Password". A canary secret is in no output of any case. `--dev` with the variables set: zero requests.
- Change `serve_test.go` l. 51 to the "settings are missing" text and exit 78.

### To verify on host (assumptions of the check)
1. `GET /version` works with the privilege-separated token and no extra right; a wrong secret and a removed token both answer 401.
2. `GET /nodes/<node>/storage` lists all three storages with `content`, `enabled`, `active` under D-081 (the snippet storage has only `Datastore.AllocateSpace`). If not: use `GET /nodes/<node>/storage/<id>/status` for each storage. If that also fails, drop this storage from the check; do not add a right.
3. The answer for a wrong node name on `/nodes/<node>/storage` (5xx, 595?). It must give the node text.
4. `GET /access/permissions` with the token: it needs no right and shows the effective rights of the token (privilege separation) with `SDN.Use` on the bridge path. If the answer differs, drop step 4 of the check.
5. The certificate of `PLUMB_PVE_URL` (`https://<host address>:8006`) has this address as a SAN. `pve-ssl.pem` has only the addresses of its creation time; a node with a second address or an ACME certificate gives the wrong-name or unknown-issuer text. Record what `pve-dev` gives.
6. The answer of `GET /pools?poolid=<wrong>` (empty list, or an error "does not exist"). Both must give the pool text.

### Acceptance on the real host (`pve-dev`; the agent that `pl-lead` names; AGENTS.md "Test host")
The host has the install of T-013 run 3 (LXC, key, `plumb.env`). So this is a **repair run**: no `plumb key init`, no TTY needed.
1. `make build` on the T-053 branch. `install.sh` is the one of PR #26 at the commit that `pl-lead` names in the go (at plan time: `origin/pl-backend/T-013-minimal-installer` 5a4fd44); write the commit into the log. `install.sh --plumb-binary bin/plumb --dry-run`; send the result to `pl-lead`; wait for the go; then the real run.
2. `plumb.service` is active and does not restart. The journal has the Proxmox line and "Plumb listens on".
3. The journal has the "First start. User: admin  Password:" line exactly once; a restart of the service adds no second line.
4. The snippet on the host holds the CA of `/var/lib/plumb/link/ca.crt` and `https://<LXC address>:8444` (mode 0644, root). It replaces the throwaway CA of T-013 step 5.
5. `https://<LXC address>:8443` answers with a certificate of the Plumb CA (`curl --cacert`; `browser-shot` if `mobtest` has a route). Log in as `admin`, change the password. Put the new password and one API token into 0600 files in `~/.config/plump/`.
6. Negative starts. Run exactly this in the LXC, as root on the host, once for each case. No `set -x`, no `env`, no `cat` of `plumb.env`. `<override>` is the only change:
   ```sh
   pct exec <LXC id> -- runuser -u plumb -- sh -c '
     d=$(mktemp -d) || exit 99
     set -a; . /etc/plumb/plumb.env; set +a
     <override>
     timeout 30 /usr/local/bin/plumb serve --data "$d" \
       --key-file /var/lib/plumb/key/master.key \
       --listen 127.0.0.1:18443 --agent-listen 127.0.0.1:18444
     echo "exit $?"; ls -A "$d"; rm -rf "$d"'
   ```
   | Case | `<override>` | Wanted |
   |---|---|---|
   | wrong secret | `PLUMB_PVE_SECRET=00000000-0000-0000-0000-000000000000` | bad-token text, exit 78 |
   | wrong CA | `PLUMB_PVE_CA=/var/lib/plumb/link/ca.crt` (the Plumb CA) | unknown-issuer text, exit 78 |
   | wrong pool | `PLUMB_PVE_POOL=plumb-none` | pool text, exit 78 |
   | wrong image storage | `PLUMB_PVE_IMAGES=local` | storage text, exit 78 |
   | wrong node | `PLUMB_PVE_NODE=no-such-node` | node text, exit 78 |
   - The key file is only read: every case stops before `store.Open`. A temp `--data` has no key, so `--key-file` names the real one.
   - The ports are not 8443/8444. A case that passes by mistake cannot clash with the service; `timeout` ends it with exit 124. That is a failed case: report it to `pl-lead`.
   - `ls -A` must print nothing. `rm -rf "$d"` always runs: a database that a wrong pass made is sealed with the real master key.
   - `plumb.env`, the service and the token stay untouched.
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
