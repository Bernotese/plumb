<script lang="ts">
  // Settings → Backups (T-118; D-288 … D-290, D-302 … D-304): the backups of
  // this controller, the off-host copy, "back up now", and the restore state
  // with the confirm that ends restored mode.
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { bytesText } from "../lib/images";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { BackupList, RestoreState } from "../lib/types";
  import { formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import ResetPassword from "../ui/ResetPassword.svelte";

  let { api, onRestore }: { api: PlumbApi; onRestore?: (r: RestoreState) => void } = $props();

  let list = $state<BackupList | null>(null);
  let restore = $state<RestoreState | null>(null);
  let error = $state<unknown>(null);
  let actionError = $state<unknown>(null);
  let busy = $state(false);
  let confirmOff = $state(false);
  let reason = $state("");
  let confirmError = $state<unknown>(null);
  let offError = $state("");

  $effect(() => {
    const live = liveSnapshot({
      load: () => Promise.all([api.listBackups(), api.getRestore()]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([b, r]) => {
        list = b;
        restore = r;
        onRestore?.(r);
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (
          e.type === "backup.finished" ||
          e.type === "restore.confirmed" ||
          e.type === "token.enabled" ||
          e.type === "user.password_reset" ||
          e.type === "settings.changed"
        )
          l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const COPY: Record<BackupList["copy"]["state"], { label: string; tone: Tone; text: string }> = {
    none: {
      label: "No copy",
      tone: "warn",
      text: "No backup leaves this container. Run the installer with --backup-storage to copy each backup to a storage of the host.",
    },
    ok: { label: "Copied", tone: "ok", text: "The last backup was copied off the container." },
    failed: { label: "Copy failed", tone: "danger", text: "The last copy failed." },
    stale: {
      label: "Copy old",
      tone: "warn",
      text: "The last good copy is older than 36 hours.",
    },
  };
  const copyLine = (c: BackupList["copy"]) =>
    `Last copy: ${formatTime(c.at)}${c.storage ? ` to ${c.storage}` : ""}.`;
  const RESULT: Record<string, Tone> = { running: "info", ok: "ok", failed: "danger" };

  // A user with a new password leaves the locked list (D-321).
  async function reloadRestore() {
    try {
      restore = await api.getRestore();
      onRestore?.(restore);
    } catch (e) {
      error = e;
    }
  }

  async function backUpNow() {
    actionError = null;
    busy = true;
    try {
      await api.createBackup();
      list = await api.listBackups();
    } catch (e) {
      actionError = e;
    } finally {
      busy = false;
    }
  }

  async function confirm(ev: SubmitEvent) {
    ev.preventDefault();
    confirmError = null;
    offError = confirmOff ? "" : "Confirm that the old controller is off.";
    if (offError) {
      document.getElementById("restore-off")?.focus();
      return;
    }
    busy = true;
    try {
      restore = await api.confirmRestore(reason);
      onRestore?.(restore);
      // The form is gone: keep the focus in the card (review L4 of #227).
      await tick();
      document.getElementById("restore-title")?.focus();
    } catch (e) {
      confirmError = e;
    } finally {
      busy = false;
    }
  }
</script>

<ProblemBox {error} />
{#if list && restore}
  {#if restore.restored}
    <section class="card" aria-labelledby="restore-title" class:alert={restore.restored_mode}>
      <h2 id="restore-title" tabindex="-1">Restore</h2>
      <dl class="facts">
        <dt>Backup from</dt>
        <dd>{formatTime(restore.backup_created_at)}</dd>
        <dt>Restored at</dt>
        <dd>{formatTime(restore.restored_at)}</dd>
        {#if restore.confirmed_at}
          <dt>Confirmed</dt>
          <dd>{formatTime(restore.confirmed_at)} by {restore.confirmed_by}</dd>
        {/if}
      </dl>
      <p>
        Changes after the backup are lost. Secrets have their values from the time of the backup.
      </p>
      {#if restore.restored_mode}
        <p>
          <strong>Restored mode.</strong> Plumb reads Proxmox and the agents, but it changes nothing:
          no Plan is applied, and no backup, image build or re-enrollment runs. If the old controller
          still runs, two controllers would change the same guests.
        </p>
        <form onsubmit={confirm} novalidate>
          <div class="field">
            <label class="check">
              <input
                type="checkbox"
                id="restore-off"
                bind:checked={confirmOff}
                aria-invalid={!!offError}
                aria-describedby={offError ? "restore-off-error" : undefined}
              />
              The old controller is off, and it will not start again.
            </label>
            {#if offError}<span class="error" id="restore-off-error">{offError}</span>{/if}
          </div>
          <div class="field">
            <label for="restore-reason">Reason (optional)</label>
            <textarea id="restore-reason" bind:value={reason} maxlength="2000"></textarea>
          </div>
          <ProblemBox error={confirmError} />
          <button class="btn primary" type="submit" disabled={busy}>End restored mode</button>
          <p class="muted">
            After it, Plumb applies approved Plans again and sends the agents their work.
          </p>
        </form>
      {/if}
      {#if restore.disabled_tokens}
        <p id="restore-tokens">
          {restore.disabled_tokens} API token{restore.disabled_tokens === 1 ? " is" : "s are"} disabled
          by the restore. Enable only the ones you still need:
          <a href={href({ name: "settings", tab: "tokens" })}>API tokens</a>.
        </p>
      {/if}
      {#if restore.locked_users}
        <h3 id="restore-otp-users-title" tabindex="-1">Locked users</h3>
        <p class="muted">
          The restore locked every user but admin. Give a user a one-time password to let them in
          again; they choose their own password at the first login.
        </p>
        <ResetPassword
          {api}
          id="restore-otp"
          users={restore.locked_users}
          onReset={reloadRestore}
        />
      {/if}
      {#if restore.unknown_guests?.length}
        <h3>Unknown guests</h3>
        <p class="muted">
          These guests are in the Plumb pool, but the backup does not know them (they were made
          after it). Plumb never changes or deletes them.
        </p>
        <ul id="unknown-guests">
          {#each restore.unknown_guests as g (g.guest_id)}
            <li>
              <span class="mono">{g.guest_id}</span>
              {g.name} · {g.type === "lxc" ? "container" : "VM"}{g.template ? " template" : ""}
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}

  <section class="card" aria-labelledby="backups-title">
    <h2 id="backups-title">Backups</h2>
    <p>
      Plumb backs up its whole state every day at {list.schedule.time} UTC and keeps {list.schedule
        .keep} backups. A backup is encrypted. Only the recovery key opens it.
      <a href={href({ name: "settings", tab: "general" })} id="backup-schedule-link"
        >Change the schedule</a
      >
    </p>
    <dl class="facts">
      <dt>Last run</dt>
      <dd id="backup-last">
        {#if list.last_run}
          <Badge tone={RESULT[list.last_run.result] ?? "muted"} label={list.last_run.result} />
          {formatTime(list.last_run.started_at)} by {list.last_run.by}{#if list.last_run.message}
            · {list.last_run.message}{/if}
        {:else}
          No backup yet.
        {/if}
      </dd>
      <dt>Off-host copy</dt>
      <dd id="backup-copy">
        <Badge tone={COPY[list.copy.state].tone} label={COPY[list.copy.state].label} />
        {COPY[list.copy.state].text}
        {#if list.copy.at}{copyLine(list.copy)}{/if}
        {#if list.copy.message}<span class="mono">{list.copy.message}</span>{/if}
      </dd>
    </dl>
    <ProblemBox error={actionError} />
    <button
      class="btn"
      type="button"
      id="backup-now"
      disabled={busy || list.running || restore.restored_mode}
      onclick={backUpNow}>{list.running ? "A backup runs…" : "Back up now"}</button
    >
    {#if restore.restored_mode}<p class="muted">Backups wait until restored mode ends.</p>{/if}

    {#if list.items.length}
      <table>
        <caption class="sr-only">Backups in the container, newest first</caption>
        <thead>
          <tr>
            <th scope="col">File</th>
            <th scope="col">Written</th>
            <th scope="col">Size</th>
          </tr>
        </thead>
        <tbody>
          {#each list.items as b (b.name)}
            <tr>
              <th scope="row" class="mono">{b.name}</th>
              <td>{formatTime(b.created_at)}</td>
              <td>
                {bytesText(b.size)}
                {#if b.state === "unreadable"}
                  <span class="warn"
                    >· Plumb cannot read this file. Give it to the user plumb in the container.</span
                  >
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="muted">There is no backup in the container yet.</p>
    {/if}
    <p class="muted">
      There is no download: a backup holds the CA key and the agent identities. It leaves the
      container only through the copy on the host.
    </p>
  </section>
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .warn {
    color: var(--color-warn);
  }
  .card h2 {
    margin-top: 0;
  }
  .alert {
    border-left: 4px solid var(--color-warn);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .facts dt {
    font-weight: 600;
  }
  .facts dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  table {
    margin-top: var(--space-3);
  }
  th.mono {
    font-weight: 400;
    overflow-wrap: anywhere;
  }
</style>
