<script lang="ts">
  // The guest backups of one Target (T-223, ARCHITECTURE §20, D-405 … D-411, D-417):
  // the storage and its encryption, the schedule, "back up now", the volumes
  // by kind, the delete of one volume (admin person) and the restore wizard.
  import { tick } from "svelte";
  import {
    settingsRisks,
    type GuestBackup,
    type GuestBackupList,
    type PlumbApi,
    type SettingsRisks,
  } from "../lib/client";
  import { bytesText } from "../lib/images";
  import { liveSnapshot } from "../lib/live";
  import { go, href } from "../lib/router";
  import { can, me, roleNote } from "../lib/session.svelte";
  import type { TargetView } from "../lib/types";
  import { ALLOW_RESTART_LABEL, forKind, formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, name }: { api: PlumbApi; name: string } = $props();
  let target = $state<TargetView | null>(null);
  let list = $state<GuestBackupList | null>(null);
  let error = $state<unknown>(null);
  // M2 of #321, M1/M2 of #330: what the token could do beyond the install (person only).
  let risks = $state<SettingsRisks | null>(null);
  const otherStorages = $derived(risks?.otherStorages ?? []);

  $effect(() => {
    const live = liveSnapshot({
      load: () => Promise.all([api.getTarget(name), api.listGuestBackups(name)]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([t, l]) => {
        target = t;
        list = l;
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.subject === `target:${name}` || e.type === "run.finished") l.reloadSoon();
      },
    });
    return () => live.stop();
  });
  $effect(() => {
    api.getSettings().then(
      (s) => (risks = settingsRisks(s)),
      // Extra information; the page works without it.
      () => {},
    );
  });

  const STATE = {
    "pbs-key": { tone: "ok", label: "encrypted" },
    "stated-clear": { tone: "warn", label: "not encrypted" },
    "clear-refused": { tone: "danger", label: "refused: not encrypted" },
  } as const;
  const RESULT = {
    running: { tone: "muted", label: "running" },
    done: { tone: "ok", label: "done" },
    warning: { tone: "warn", label: "warning" },
    failed: { tone: "danger", label: "failed" },
    refused: { tone: "danger", label: "refused" },
  } as const;
  const TRIGGER = {
    scheduled: "scheduled",
    manual: "back up now",
    "pre-restore": "safety backup before a restore",
    unknown: "–",
  } as const;
  const MODE = { snapshot: "snapshot", stop: "stop", suspend: "suspend", unknown: "–" } as const;
  const WEEKDAY = {
    mon: "Monday",
    tue: "Tuesday",
    wed: "Wednesday",
    thu: "Thursday",
    fri: "Friday",
    sat: "Saturday",
    sun: "Sunday",
  } as const;

  const schedule = $derived(target?.document.spec.backup ?? null);
  const scheduleText = $derived(
    schedule
      ? `${schedule.schedule === "weekly" ? `Every ${WEEKDAY[schedule.weekday ?? "sun"]}` : "Every day"} at ${schedule.time} UTC. Plumb keeps the newest ${schedule.keep} scheduled backup${schedule.keep === 1 ? "" : "s"} and, apart from them, at most 2 made by "Back up now".`
      : "",
  );
  const adopted = $derived(target?.document.spec.adopted !== undefined);
  const items = $derived(list?.items ?? []);
  const byKind = (k: GuestBackup["kind"]) => items.filter((b) => b.kind === k);
  const running = $derived(list?.last?.result === "running");
  // D-409: the delete of one volume is for a person with the role admin, never a token.
  // Without a known session the button stays hidden (review of #338).
  const canDelete = $derived(
    can("deleteGuestBackup") && (me.session?.principal.startsWith("user:") ?? false),
  );
  // Review L1 of #338: no job on a refused storage, and say why.
  const nowOff = $derived(
    !list?.storage
      ? "Off: the install named no guest backup storage."
      : list.storage.state === "clear-refused"
        ? "Off: the storage does not encrypt, and the install did not allow backups in clear text."
        : "",
  );

  // --- Back up now (D-408) ---
  let nowReason = $state("");
  let nowBusy = $state(false);
  let nowError = $state<unknown>(null);
  let nowStarted = $state(false);
  async function backUpNow(ev: SubmitEvent) {
    ev.preventDefault();
    nowBusy = true;
    nowError = null;
    nowStarted = false;
    try {
      await api.backUpGuestNow(name, nowReason);
      nowReason = "";
      nowStarted = true;
      list = await api.listGuestBackups(name);
    } catch (err) {
      nowError = err;
    } finally {
      nowBusy = false;
    }
  }

  // --- Delete of one volume (D-409) ---
  let confirmId = $state<string | null>(null);
  let deleteError = $state<unknown>(null);
  let deleteBusy = $state(false);
  async function askDelete(id: string) {
    confirmId = id;
    deleteError = null;
    await tick();
    document.getElementById(`delete-cancel-${id}`)?.focus();
  }
  async function remove(id: string) {
    deleteBusy = true;
    deleteError = null;
    try {
      await api.deleteGuestBackup(name, id);
      confirmId = null;
      list = await api.listGuestBackups(name);
      await tick();
      document.getElementById("page-title")?.focus();
    } catch (err) {
      deleteError = err;
    } finally {
      deleteBusy = false;
    }
  }

  // --- Restore wizard (D-410, D-411, D-417) ---
  let restoreOf = $state<GuestBackup | null>(null);
  let restoreReason = $state("");
  let restoreAck = $state(false);
  let restoreErrors = $state<Record<string, string>>({});
  let restoreError = $state<unknown>(null);
  let restoreBusy = $state(false);
  async function startRestore(b: GuestBackup) {
    restoreOf = b;
    restoreReason = "";
    restoreAck = false;
    restoreErrors = {};
    restoreError = null;
    await tick();
    document.getElementById("restore-title")?.focus();
  }
  async function cancelRestore() {
    const id = restoreOf?.id;
    restoreOf = null;
    await tick();
    if (id) document.getElementById(`restore-${id}`)?.focus();
  }
  async function proposeRestore(ev: SubmitEvent) {
    ev.preventDefault();
    if (!restoreOf) return;
    const e: Record<string, string> = {};
    if (!restoreAck) e["ack"] = "Confirm that you know what is lost.";
    if (!restoreReason.trim()) e["reason"] = "Say why you restore the guest.";
    restoreErrors = e;
    if (Object.keys(e).length) {
      await tick();
      (document.querySelector('#restore [aria-invalid="true"]') as HTMLElement | null)?.focus();
      return;
    }
    restoreBusy = true;
    restoreError = null;
    try {
      const plan = await api.restoreGuest(name, {
        backup: restoreOf.id,
        reason: restoreReason.trim(),
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      restoreError = err;
    } finally {
      restoreBusy = false;
    }
  }
</script>

<p class="back">
  <a href={href({ name: "target-edit", target: name })}>← Target {name}</a> ·
  <a href={href({ name: "target-agent", target: name })}>Agent</a>
</p>
<h1 id="page-title" tabindex="-1">Backups of <span class="mono">{name}</span></h1>
<ProblemBox {error} />

{#if target && list}
  <section class="card" aria-labelledby="storage-title" id="backup-storage">
    <h2 id="storage-title">Storage</h2>
    {#if list.storage}
      {@const st = STATE[list.storage.state]}
      <p>
        <span class="mono">{list.storage.id}</span>
        <Badge tone={st.tone} label={st.label} />
        {#if list.storage.free_pct !== null}
          <span class:warn={list.storage.free_pct < 10} id="backup-free"
            >{list.storage.free_pct} % free</span
          >
        {/if}
      </p>
      {#if list.storage.state === "pbs-key"}
        <p id="backup-encryption">
          Proxmox Backup Server encrypts each backup with a client key before it leaves the host.
        </p>
      {:else if list.storage.state === "stated-clear"}
        <p class="banner warn" role="note" id="backup-encryption">
          <strong>These backups are not encrypted.</strong> A backup holds the whole guest disk,
          with every file and secret in the guest. Anyone who can read the storage can read them.
          The install allowed this (<code>--guest-backup-clear</code>).
        </p>
      {:else}
        <p class="banner danger" role="note" id="backup-encryption">
          <strong>Every backup is refused.</strong> The storage does not encrypt, and the install
          did not allow backups in clear text. Use a Proxmox Backup Server storage with an
          encryption key, or run the installer again with <code>--guest-backup-clear</code>.
        </p>
      {/if}
      {#if list.storage.free_pct !== null && list.storage.free_pct < 10}
        <p class="warn" id="backup-low-space">
          Less than 10 % of the storage is free: Plumb refuses new backups until there is more
          space.
        </p>
      {/if}
    {:else}
      <p id="backup-no-storage">
        The install named no guest backup storage, so Plumb makes no guest backups. Run the
        installer again with <code>--guest-backup-storage</code> to turn them on.
      </p>
    {/if}
    {#if otherStorages.length}
      <p class="banner danger" role="note" id="backup-other-storages">
        <strong>The token can also write backups to {otherStorages.join(", ")}.</strong> Only one storage
        may take backups. Remove the content type "backup" there, or the Plumb rights on it.
      </p>
    {/if}
    {#if risks?.diskStorage}
      <p class="banner danger" role="note" id="backup-disk-content">
        <strong
          >The backup storage {risks.diskStorage} also takes guest disks ({risks.diskContent.join(
            ", ",
          )}).</strong
        >
        The token could put guest disks there. Remove these content types from the storage (Datacenter
        → Storage, or <code>pvesm set {risks.diskStorage} --content</code> with the list without them).
      </p>
    {/if}
    {#if risks?.aclExtra.length}
      <div class="banner danger" role="note" id="backup-acl-extra">
        <p>
          <strong>The token has rights beyond the install.</strong> Run the installer again with the same
          flags (a repair run); it removes them.
        </p>
        <ul>
          {#each risks.aclExtra as a (a.path + a.role)}<li>
              <code>{a.path}</code> ({a.role})
            </li>{/each}
        </ul>
      </div>
    {/if}
    <p class="muted" id="backup-token-power">
      The Proxmox API token of Plumb can itself back up, restore over and delete the backups of
      every guest in the pool. The rules of Plumb (for example: only a person restores) hold only
      inside Plumb.
    </p>
  </section>

  <section class="card" aria-labelledby="schedule-title" id="backup-schedule">
    <h2 id="schedule-title">Schedule</h2>
    {#if schedule}
      <p id="schedule-text">{scheduleText}</p>
      {#if list.next_at}<p>Next backup: {formatTime(list.next_at)}</p>{/if}
    {:else}
      <p id="schedule-text">
        No schedule: Plumb makes backups of this guest only with "Back up now". Set a schedule in
        the <a href={href({ name: "target-edit", target: name })}>Target form</a>.
      </p>
    {/if}
    {#if list.last}
      {@const r = RESULT[list.last.result]}
      <p id="backup-last">
        Last backup: {formatTime(list.last.at)}
        <Badge tone={r.tone} label={r.label} />
        {#if list.last.message}<span class="muted">{list.last.message}</span>{/if}
      </p>
    {/if}
    <p class="muted">
      A backup does not change the guest. A VM without the QEMU guest agent is backed up like after
      a power cut: open files can be half written.
    </p>
    {#if can("backUpGuestNow")}
      <form onsubmit={backUpNow} novalidate>
        <div class="field">
          <label for="backup-now-reason">Reason (optional)</label>
          <input id="backup-now-reason" type="text" maxlength="2000" bind:value={nowReason} />
        </div>
        <ProblemBox error={nowError} />
        <button
          class="btn primary"
          type="submit"
          id="backup-now"
          disabled={nowBusy || running || nowOff !== ""}
          aria-describedby={nowOff ? "backup-now-off" : undefined}>Back up now</button
        >
        {#if nowOff}<span class="muted" id="backup-now-off">{nowOff}</span>{/if}
        <span role="status" class="muted"
          >{running ? "A backup runs." : nowStarted ? "The backup started." : ""}</span
        >
      </form>
    {:else}
      <p class="muted" id="backup-now-role">{roleNote("apply")}</p>
    {/if}
  </section>

  {#snippet table(rows: GuestBackup[], caption: string, actions: boolean)}
    <table class="list">
      <caption class="sr-only">{caption}</caption>
      <thead>
        <tr>
          <th scope="col">Time</th>
          <th scope="col">Made by</th>
          <th scope="col">Mode</th>
          <th scope="col">Size</th>
          {#if actions}<th scope="col">Actions</th>{/if}
        </tr>
      </thead>
      <tbody>
        {#each rows as b (b.id)}
          <tr id="backup-{b.id}">
            <th scope="row">{formatTime(b.time)}</th>
            <td data-label="Made by">{TRIGGER[b.trigger]}</td>
            <td data-label="Mode">{MODE[b.mode]}</td>
            <td data-label="Size">{bytesText(b.size)}</td>
            {#if actions}
              <td data-label="Actions">
                {#if confirmId === b.id}
                  <div class="confirm" role="group" aria-label="Confirm delete of the backup">
                    <span
                      >Delete the backup of {formatTime(b.time)}? It is gone for good. This cannot
                      be undone.</span
                    >
                    <div class="row">
                      <button
                        id="delete-confirm-{b.id}"
                        class="btn danger"
                        type="button"
                        disabled={deleteBusy}
                        onclick={() => remove(b.id)}>Delete backup</button
                      >
                      <button
                        id="delete-cancel-{b.id}"
                        class="btn"
                        type="button"
                        onclick={() => (confirmId = null)}>Cancel</button
                      >
                    </div>
                  </div>
                {:else}
                  <div class="row">
                    {#if b.kind === "plumb" && can("restoreGuest")}
                      <button
                        class="btn"
                        type="button"
                        id="restore-{b.id}"
                        aria-label="Restore the backup of {formatTime(b.time)}"
                        onclick={() => startRestore(b)}>Restore…</button
                      >
                    {/if}
                    {#if canDelete}
                      <button
                        class="btn"
                        type="button"
                        id="delete-{b.id}"
                        aria-label="Delete the backup of {formatTime(b.time)}"
                        onclick={() => askDelete(b.id)}>Delete</button
                      >
                    {/if}
                  </div>
                {/if}
              </td>
            {/if}
          </tr>
        {/each}
      </tbody>
    </table>
  {/snippet}

  <section class="card" aria-labelledby="items-title" id="backup-items">
    <h2 id="items-title">Backups of this guest</h2>
    <ProblemBox error={deleteError} />
    {#if byKind("plumb").length}
      {@render table(byKind("plumb"), "Backups of this guest", can("restoreGuest") || canDelete)}
    {:else}
      <p class="muted" id="backup-none">No backup of this guest yet.</p>
    {/if}
    {#if !canDelete}
      <p class="muted" id="backup-delete-role">
        Only a person with the role admin deletes a backup.
      </p>
    {/if}

    {#if byKind("older-guest").length}
      <h3 id="older-title">Backups of an older guest</h3>
      <p class="muted">
        Plumb made these for an older guest of this Target, for example before the guest was made
        again. Plumb never restores them and its retention never deletes them. A person may delete
        them.
      </p>
      {@render table(byKind("older-guest"), "Backups of an older guest", canDelete)}
    {/if}
    {#if byKind("unmarked").length}
      <h3 id="unmarked-title">Backups that Plumb does not know</h3>
      <p class="muted">
        This Plumb has no record of these: a person made them, the mark failed or was copied, or the
        record was lost with a restore of Plumb. Plumb never restores or deletes them. Use Proxmox
        for them.
      </p>
      {@render table(byKind("unmarked"), "Backups that Plumb does not know", false)}
    {/if}
    <p class="muted" id="backup-after-delete">
      After a delete of the Target the backups stay in Proxmox, and Plumb forgets them: a new Target
      of this name never restores or deletes them. Remove them in Proxmox when you no longer need
      them.
    </p>
  </section>

  {#if restoreOf}
    {@const b = restoreOf}
    <form
      class="card"
      id="restore"
      aria-labelledby="restore-title"
      onsubmit={proposeRestore}
      novalidate
    >
      <h2 id="restore-title" tabindex="-1">Restore the backup of {formatTime(b.time)}</h2>
      <p class="banner danger" role="note" id="restore-human-only">
        <strong>Only a person can approve a restore.</strong> This makes a plan with the risk destructive.
        A person with the role admin approves it; an API token never can.
      </p>
      <p>What happens in the Run:</p>
      <ol id="restore-steps">
        <li>
          Plumb stops the guest. A running guest needs "{forKind(
            ALLOW_RESTART_LABEL,
            target.document.spec.type ?? "vm",
          )}" on the Target, or stop it yourself first; else Plumb refuses the restore.
        </li>
        <li>
          Plumb revokes the agent credential and drops the pinned SSH host keys before the guest
          starts again.
        </li>
        <li>Proxmox overwrites the whole guest with the backup.</li>
        {#if adopted}
          <li>
            Plumb starts the guest without an agent. Then make an install command on the
            <a href={href({ name: "target-agent", target: name })}>agent page</a> and run it in the guest.
          </li>
        {:else}
          <li>
            A re-enrollment plan follows. The guest stays stopped until you approve it; then it
            starts with a new agent credential.
          </li>
        {/if}
      </ol>
      <p>What is lost and what comes back:</p>
      <ul id="restore-lost">
        <li>
          <strong>Everything written in the guest after {formatTime(b.time)} is lost.</strong>
        </li>
        <li>
          Access comes back as it was then: SSH keys, passwords and users that a person removed
          later work again.
        </li>
        <li>
          The Target document is not rolled back. The next Run sets the guest to the current
          document again.
        </li>
      </ul>
      <p class="muted" id="restore-gone">
        Plumb restores only over this guest. For a guest that is gone, restore the backup in Proxmox
        into the pool <code>plumb</code>, then adopt the guest.
      </p>
      <div class="field">
        <label class="check">
          <input
            type="checkbox"
            id="restore-ack"
            bind:checked={restoreAck}
            aria-invalid={restoreErrors["ack"] ? "true" : undefined}
            aria-describedby={restoreErrors["ack"] ? "restore-ack-error" : undefined}
          />
          I know that everything after {formatTime(b.time)} is lost.
        </label>
        {#if restoreErrors["ack"]}<p class="error" id="restore-ack-error">
            {restoreErrors["ack"]}
          </p>{/if}
      </div>
      <div class="field">
        <label for="restore-reason">Reason</label>
        <textarea
          id="restore-reason"
          bind:value={restoreReason}
          maxlength="2000"
          aria-invalid={restoreErrors["reason"] ? "true" : undefined}
          aria-describedby={restoreErrors["reason"] ? "restore-reason-error" : undefined}
        ></textarea>
        {#if restoreErrors["reason"]}<p class="error" id="restore-reason-error">
            {restoreErrors["reason"]}
          </p>{/if}
      </div>
      <ProblemBox error={restoreError} />
      <div class="row">
        <button class="btn danger" type="submit" id="restore-propose" disabled={restoreBusy}
          >Propose restore</button
        >
        <button class="btn" type="button" onclick={cancelRestore}>Cancel</button>
      </div>
    </form>
  {/if}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .warn {
    color: var(--color-warn);
  }
  ol,
  ul {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .confirm {
    display: grid;
    gap: var(--space-2);
  }
</style>
