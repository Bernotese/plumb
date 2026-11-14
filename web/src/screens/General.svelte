<script lang="ts">
  // Settings → General (T-176, D-361): the backup schedule, editable by a
  // human admin, with the source of each value; and a read-only view of the
  // Proxmox connection. No secret is in the answer: the token shows only
  // its user part (`<user>@<realm>`).
  import { tick } from "svelte";
  import type { ChangeSettings, PlumbApi, RepairKeyView, Settings } from "../lib/client";
  import { href } from "../lib/router";
  import { describedBy, pointerErrors } from "../lib/formErrors";
  import { liveSnapshot } from "../lib/live";
  import { restored, RESTORED_WAIT } from "../lib/restored.svelte";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();

  let settings = $state<Settings | null>(null);
  let loadError = $state<unknown>(null);
  let time = $state("");
  let keep = $state("");
  let errors = $state<Record<string, string>>({});
  let saveError = $state<unknown>(null);
  let saved = $state("");
  let busy = $state(false);
  let dirty = false;

  const TIME = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;
  const SOURCE = {
    default: "built-in default",
    env: "from the install (flag or environment)",
    setting: "set here",
  } as const;

  function show(s: Settings) {
    settings = s;
    if (!dirty) {
      time = s.backup.time;
      keep = String(s.backup.keep);
    }
  }

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.getSettings(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (s) => {
        show(s);
        loadError = null;
      },
      onEvent: (e, l) => {
        if (e.type === "settings.changed") l.reloadSoon();
      },
      onError: (e) => (loadError = e),
    });
    return () => live.stop();
  });

  function check(): ChangeSettings["backup"] | null {
    const e: Record<string, string> = {};
    if (!TIME.test(time.trim())) e["time"] = "Give a time as HH:MM (UTC), for example 03:00.";
    const n = Number(keep);
    if (!Number.isInteger(n) || n < 1 || n > 100) e["keep"] = "Keep 1 to 100 backups.";
    errors = e;
    if (Object.keys(e).length) return null;
    const b: NonNullable<ChangeSettings["backup"]> = {};
    if (time.trim() !== settings?.backup.time) b.time = time.trim();
    if (n !== settings?.backup.keep) b.keep = n;
    return b;
  }

  async function send(backup: NonNullable<ChangeSettings["backup"]>) {
    busy = true;
    saveError = null;
    saved = "";
    try {
      dirty = false;
      show(await api.changeSettings({ backup }));
      saved = `Saved. The next backup runs at ${settings?.backup.time} UTC; Plumb keeps ${settings?.backup.keep}.`;
    } catch (err) {
      saveError = err;
      const p = pointerErrors(err);
      errors = {
        ...(p["/backup/time"] ? { time: p["/backup/time"] } : {}),
        ...(p["/backup/keep"] ? { keep: p["/backup/keep"] } : {}),
      };
    } finally {
      busy = false;
    }
  }

  async function save(ev: SubmitEvent) {
    ev.preventDefault();
    const b = check();
    if (!b) {
      await tick();
      (
        document.querySelector('#general-backup [aria-invalid="true"]') as HTMLElement | null
      )?.focus();
      return;
    }
    if (Object.keys(b).length === 0) {
      saved = "Nothing to save: the values are the same.";
      return;
    }
    await send(b);
  }

  // --- Repair key (D-382, T-194): rotate, then revoke the previous key ---
  let keyBusy = $state(false);
  let keyError = $state<unknown>(null);
  let confirmRevoke = $state(false);
  const repairKey = $derived<RepairKeyView | undefined>(settings?.repair_key ?? undefined);
  async function rotate(revokeOld: boolean) {
    keyBusy = true;
    keyError = null;
    try {
      const k = await api.rotateRepairKey(revokeOld);
      if (settings) settings = { ...settings, repair_key: k };
      confirmRevoke = false;
      await tick();
      document.getElementById("general-key-title")?.focus();
    } catch (err) {
      keyError = err;
    } finally {
      keyBusy = false;
    }
  }

  /** `null` removes the stored value: the install value counts again. */
  const reset = (field: "time" | "keep") => send({ [field]: null });

  const dash = (v: string | number | null | undefined) =>
    v === null || v === undefined || v === "" ? "–" : String(v);
  const px = $derived(settings?.proxmox ?? null);
  const noProxmox = $derived(
    px !== null && px.node === null && px.api.host === null && px.version === null,
  );
  const ACL = {
    ok: { tone: "ok", label: "all rights there" },
    missing: { tone: "danger", label: "rights missing" },
    unknown: { tone: "warn", label: "unknown" },
  } as const;
</script>

<ProblemBox error={loadError} />
{#if settings}
  <section class="card" aria-labelledby="general-backup-title" id="general-backup">
    <h2 id="general-backup-title">Backup schedule</h2>
    <p class="muted">
      Plumb backs up its whole state once a day. A value set here wins over the value of the
      install; a repair run of the installer keeps it. The scheduler takes a change at once.
    </p>
    <form onsubmit={save} novalidate>
      <div class="grid">
        <div class="field">
          <label for="g-time">Time of day (UTC)</label>
          <input
            id="g-time"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            placeholder="03:00"
            bind:value={time}
            oninput={() => (dirty = true)}
            aria-invalid={!!errors["time"]}
            aria-describedby={describedBy("g-time", errors["time"])}
          />
          <span class="hint" id="g-time-hint"
            >HH:MM, 24 hours. Now: {SOURCE[settings.backup.source.time]}. If you move the time to
            one that has already passed today, the next backup runs tomorrow: today has none.</span
          >
          {#if errors["time"]}<span class="error" id="g-time-error">{errors["time"]}</span>{/if}
          {#if settings.backup.source.time === "setting"}<button
              type="button"
              class="link"
              id="g-time-reset"
              disabled={busy || restored.mode}
              onclick={() => reset("time")}>Use the install value again</button
            >{/if}
        </div>
        <div class="field">
          <label for="g-keep">Backups to keep</label>
          <input
            id="g-keep"
            type="text"
            inputmode="numeric"
            autocomplete="off"
            bind:value={keep}
            oninput={() => (dirty = true)}
            aria-invalid={!!errors["keep"]}
            aria-describedby={describedBy("g-keep", errors["keep"])}
          />
          <span class="hint" id="g-keep-hint"
            >1 to 100, scheduled and manual backups together. Now: {SOURCE[
              settings.backup.source.keep
            ]}.</span
          >
          {#if errors["keep"]}<span class="error" id="g-keep-error">{errors["keep"]}</span>{/if}
          {#if settings.backup.source.keep === "setting"}<button
              type="button"
              class="link"
              id="g-keep-reset"
              disabled={busy || restored.mode}
              onclick={() => reset("keep")}>Use the install value again</button
            >{/if}
        </div>
      </div>
      <ProblemBox error={saveError} fields={["/backup/time", "/backup/keep"]} />
      <div class="row">
        <button
          class="btn primary"
          type="submit"
          disabled={busy || restored.mode}
          aria-describedby={restored.mode ? "g-restored" : undefined}>Save schedule</button
        >
      </div>
      {#if restored.mode}<p class="muted" id="g-restored">{RESTORED_WAIT}</p>{/if}
      <p class="ok" role="status" id="g-saved">{saved}</p>
    </form>
  </section>

  {#if repairKey}
    <section class="card" aria-labelledby="general-key-title" id="general-key">
      <h2 id="general-key-title" tabindex="-1">Repair key</h2>
      <p class="muted">
        Plumb repairs a dead agent over SSH with this key (only from Plumb's own address, only the
        fixed install). Plumb never shows the private key.
      </p>
      <dl class="facts">
        <dt>Current key</dt>
        <dd class="mono wrap" id="key-current">{repairKey.fingerprint}</dd>
        <dt>Made</dt>
        <dd>{formatTime(repairKey.created_at)}</dd>
        {#if repairKey.previous}
          <dt>Previous key</dt>
          <dd class="mono wrap" id="key-previous">{repairKey.previous.fingerprint}</dd>
        {/if}
      </dl>
      {#if repairKey.left_behind.length}
        <p id="key-left-behind">
          Left behind ({repairKey.left_behind.length}): {repairKey.left_behind.join(", ")}. These
          guests are no longer Plumb's, but may still hold a repair line of a valid key. Remove the
          <code>plumb-repair-</code> line in each guest, or make a new key and revoke the old one.
        </p>
      {/if}
      {#if repairKey.previous}
        {#if repairKey.previous.targets.length}
          <p id="key-old-targets">
            Still on the previous key ({repairKey.previous.targets.length}):
            {#each repairKey.previous.targets as t, i (t)}{i ? ", " : ""}<a
                href={href({ name: "target-agent", target: t })}>{t}</a
              >{/each}. Online agents get the new key at once; an offline agent gets it when it is
            back.
          </p>
        {:else}
          <p id="key-old-targets">Every agent has the new key.</p>
        {/if}
        <ProblemBox error={keyError} />
        {#if confirmRevoke}
          <div class="confirm" role="group" aria-labelledby="revoke-q">
            <p id="revoke-q">
              <strong>Revoke the previous key?</strong>
              {repairKey.previous.targets.length
                ? `${repairKey.previous.targets.join(", ")} then ${repairKey.previous.targets.length === 1 ? "has" : "have"} no repair over SSH until the agent confirms the new key.`
                : "No agent needs it any more."}
            </p>
            <div class="row">
              <button
                class="btn danger"
                type="button"
                id="revoke-confirm"
                disabled={keyBusy}
                onclick={() => rotate(true)}>Revoke</button
              >
              <button class="btn" type="button" onclick={() => (confirmRevoke = false)}
                >Cancel</button
              >
            </div>
          </div>
        {:else}
          <button
            class="btn danger"
            type="button"
            disabled={keyBusy || restored.mode}
            onclick={() => (confirmRevoke = true)}>Revoke the previous key…</button
          >
        {/if}
      {:else}
        <ProblemBox error={keyError} />
        <button
          class="btn"
          type="button"
          id="key-rotate"
          disabled={keyBusy || restored.mode}
          onclick={() => rotate(false)}>Make a new repair key</button
        >
        <p class="muted">
          The old key stays valid until you revoke it. Online agents write the new line next to the
          old one at once; an offline agent when it is back.
        </p>
      {/if}
    </section>
  {/if}

  <section class="card" aria-labelledby="general-proxmox-title" id="general-proxmox">
    <h2 id="general-proxmox-title">Proxmox</h2>
    {#if px === null}
      <p class="muted">Only a person who logged in sees the Proxmox connection.</p>
    {:else if noProxmox}
      <p class="muted" id="px-none">This Plumb runs without Proxmox (development mode).</p>
    {:else}
      <p class="muted">
        Read-only. The installer set these values; run it again to change them. Plumb never shows
        the token name or its secret.
      </p>
      <dl class="facts">
        <dt>Node</dt>
        <dd class="mono">{dash(px.node)}</dd>
        <dt>Pool</dt>
        <dd class="mono">{dash(px.pool)}</dd>
        <dt>Storages</dt>
        <dd class="mono">
          images {dash(px.storages.images)} · snippets {dash(px.storages.snippets)} · backup {dash(
            px.storages.backup,
          )}
        </dd>
        <dt>API</dt>
        <dd class="mono">
          {dash(px.api.host)}{px.api.port ? `:${px.api.port}` : ""}
        </dd>
        <dt>TLS fingerprint</dt>
        <dd class="mono wrap">{dash(px.api.tls_sha256)}</dd>
        <dt>Proxmox version</dt>
        <dd class="mono">{dash(px.version)}</dd>
        <dt>Token user</dt>
        <dd class="mono">{dash(px.token_user)}</dd>
        <dt>Last good call</dt>
        <dd>{px.last_ok_at ? formatTime(px.last_ok_at) : "–"}</dd>
        <dt>Rights of the token</dt>
        <dd id="px-acl">
          <Badge tone={ACL[px.acl].tone} label={ACL[px.acl].label} />
          {#if px.acl === "unknown" && px.acl_error}<span class="muted"> {px.acl_error}</span>{/if}
        </dd>
      </dl>
      {#if px.acl_lines.length}
        <table class="acl">
          <caption>The rights of the install</caption>
          <thead>
            <tr><th scope="col">Path</th><th scope="col">Role</th><th scope="col">State</th></tr>
          </thead>
          <tbody>
            {#each px.acl_lines as l (`${l.path} ${l.role}`)}
              <tr>
                <td class="mono">{l.path}</td>
                <td class="mono">{l.role}</td>
                <td>
                  <Badge tone={l.ok ? "ok" : "danger"} label={l.ok ? "there" : "missing"} />
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if px.acl === "missing"}<p class="warn" id="px-acl-help">
            A right is missing: run the installer again (repair) to put it back.
          </p>{/if}
      {/if}
    {/if}
  </section>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
    gap: var(--space-3) var(--space-4);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-1) var(--space-4);
    margin: 0 0 var(--space-3);
  }
  dt {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  dd {
    margin: 0;
  }
  .wrap {
    overflow-wrap: anywhere;
  }
  .confirm {
    border-left: 4px solid var(--color-danger);
    padding-left: var(--space-3);
  }
  .confirm p {
    margin: 0 0 var(--space-2);
  }
  .acl {
    width: 100%;
    border-collapse: collapse;
  }
  .acl caption {
    text-align: left;
    font-weight: 600;
    padding-bottom: var(--space-1);
  }
  .acl th,
  .acl td {
    text-align: left;
    padding: var(--space-1) var(--space-2) var(--space-1) 0;
    border-bottom: 1px solid var(--color-border);
  }
  .ok {
    color: var(--color-ok);
    margin: var(--space-2) 0 0;
  }
  .ok:empty {
    display: none;
  }
  .warn {
    color: var(--color-warn);
  }
  .link {
    display: block;
    background: none;
    border: 0;
    padding: 0;
    margin-top: var(--space-1);
    font: inherit;
    font-size: var(--text-sm);
    font-weight: 600;
    color: inherit;
    text-decoration: underline;
    cursor: pointer;
  }
  .link:focus-visible {
    outline: 2px solid var(--color-accent);
  }
  @media (max-width: 480px) {
    .facts {
      grid-template-columns: 1fr;
    }
    .facts dd {
      margin-bottom: var(--space-2);
    }
  }
</style>
