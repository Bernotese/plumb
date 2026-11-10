<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { go } from "../lib/router";
  import type { SecretInfo } from "../lib/types";
  import { formatTime } from "../lib/view";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let secrets = $state<SecretInfo[] | null>(null);
  let error = $state<unknown>(null);

  // The form. `value` is cleared right after every request.
  let name = $state("");
  let replacing = $state(false);
  let value = $state("");
  let multiline = $state(false);
  let saveError = $state<unknown>(null);
  let busy = $state(false);
  /** After a replace of a used secret: offer to plan its targets (D-090). */
  let replaced = $state<SecretInfo | null>(null);
  let planError = $state<unknown>(null);

  let confirmName = $state<string | null>(null);
  let deleteError = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listSecrets(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (s) => (secrets = s),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type.startsWith("secret.") || e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });
  const reload = () =>
    api.listSecrets().then(
      (s) => (secrets = s),
      (e: unknown) => (error = e),
    );

  const NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;
  const nameOk = $derived(NAME.test(name));
  const exists = $derived(secrets?.some((s) => s.name === name) ?? false);

  async function startReplace(s: SecretInfo) {
    name = s.name;
    replacing = true;
    value = "";
    saveError = null;
    await tick();
    document.getElementById("secret-value")?.focus();
  }

  function resetForm() {
    name = "";
    replacing = false;
    value = "";
    multiline = false;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    saveError = null;
    replaced = null;
    const sent = value;
    value = ""; // never kept after the request
    const wasThere = exists;
    try {
      const info = await api.putSecret(name, sent);
      if (wasThere && info.used_by.length) replaced = info;
      resetForm();
      await reload();
    } catch (err) {
      saveError = err;
    } finally {
      busy = false;
    }
  }

  async function planTargets() {
    if (!replaced) return;
    planError = null;
    try {
      const plan = await api.createPlan({
        reason: `Send the new value of secret ${replaced.name}.`,
        targets: replaced.used_by,
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      planError = err;
    }
  }

  async function askDelete(n: string) {
    confirmName = n;
    deleteError = null;
    await tick();
    document.getElementById(`delete-confirm-${n}`)?.focus();
  }

  async function remove(n: string) {
    deleteError = null;
    try {
      await api.deleteSecret(n);
      confirmName = null;
      await reload();
    } catch (err) {
      deleteError = err;
    }
  }
</script>

<p class="muted">
  Plumb stores secret values encrypted. It never shows a value again, not to you and not in plans,
  logs or exports. Targets refer to a secret by its name.
</p>

{#if replaced}
  <section class="banner warn" aria-labelledby="replaced-title">
    <p id="replaced-title">
      <strong>Secret <span class="mono">{replaced.name}</span> has a new value.</strong>
      The guests get it only through a plan. Used by:
      <span class="mono">{replaced.used_by.join(", ")}</span>.
    </p>
    <ProblemBox error={planError} />
    <div class="row">
      <button class="btn primary" type="button" onclick={planTargets}>
        Plan the change for {replaced.used_by.length}
        target{replaced.used_by.length === 1 ? "" : "s"}
      </button>
      <button class="btn" type="button" onclick={() => (replaced = null)}>Later</button>
    </div>
  </section>
{/if}

<form class="card" onsubmit={save} autocomplete="off">
  <h2>{replacing ? `Replace the value of ${name}` : "Set a secret"}</h2>
  <div class="field">
    <label for="secret-name">Name</label>
    <input
      id="secret-name"
      type="text"
      class="mono"
      bind:value={name}
      readonly={replacing}
      required
      maxlength="63"
      autocomplete="off"
      spellcheck="false"
      aria-invalid={name !== "" && !nameOk}
      aria-describedby="secret-name-hint"
    />
    <span class="hint" id="secret-name-hint">
      Lower-case letters, digits, dots and dashes.
      {#if !replacing && exists}A secret with this name exists: saving replaces its value.{/if}
    </span>
  </div>
  <div class="field">
    <label for="secret-value">Value</label>
    {#if multiline}
      <textarea
        id="secret-value"
        bind:value
        required
        autocomplete="off"
        spellcheck="false"
        autocapitalize="off"
        aria-describedby="secret-value-hint"></textarea>
    {:else}
      <input
        id="secret-value"
        type="password"
        bind:value
        required
        autocomplete="off"
        spellcheck="false"
        aria-describedby="secret-value-hint"
      />
    {/if}
    <span class="hint" id="secret-value-hint">
      {multiline
        ? "Multi-line text is visible while you type. Check that nobody watches your screen."
        : "You cannot see the value again after you save it."}
    </span>
  </div>
  <div class="field check">
    <label>
      <input type="checkbox" bind:checked={multiline} />
      Multi-line value (for example an SSH or TLS key)
    </label>
  </div>
  <ProblemBox error={saveError} />
  <div class="row">
    <button class="btn primary" type="submit" disabled={busy || !nameOk || value === ""}>
      {replacing || exists ? "Replace value" : "Save secret"}
    </button>
    {#if replacing}
      <button class="btn" type="button" onclick={resetForm}>Cancel</button>
    {/if}
  </div>
</form>

<h2>Secrets</h2>
<ProblemBox {error} />
<ProblemBox error={deleteError} />
{#if secrets === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if secrets && secrets.length === 0}
  <p class="muted">No secrets yet.</p>
{:else if secrets}
  <div class="table-wrap card flush">
    <table class="stack">
      <caption class="sr-only">Secrets, by name</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Version</th>
          <th scope="col">Changed by</th>
          <th scope="col">Changed at</th>
          <th scope="col">Used by</th>
          <th scope="col">Actions</th>
        </tr>
      </thead>
      <tbody>
        {#each secrets as s (s.name)}
          <tr>
            <th scope="row" class="mono">{s.name}</th>
            <td data-label="Version">{s.version}</td>
            <td data-label="Changed by" class="mono">{s.updated_by}</td>
            <td data-label="Changed at">{formatTime(s.updated_at)}</td>
            <td data-label="Used by" class="mono">{s.used_by.join(", ") || "–"}</td>
            <td data-label="Actions">
              {#if confirmName === s.name}
                <div class="confirm" role="group" aria-label="Confirm delete of {s.name}">
                  <span>Delete "{s.name}"? The value is gone for good.</span>
                  <div class="row">
                    <button
                      id="delete-confirm-{s.name}"
                      class="btn danger"
                      type="button"
                      onclick={() => remove(s.name)}>Delete secret</button
                    >
                    <button class="btn" type="button" onclick={() => (confirmName = null)}
                      >Cancel</button
                    >
                  </div>
                </div>
              {:else}
                <div class="row">
                  <button
                    class="btn"
                    type="button"
                    aria-label="Replace {s.name}"
                    onclick={() => startReplace(s)}>Replace</button
                  >
                  <button
                    class="btn"
                    type="button"
                    disabled={s.used_by.length > 0}
                    aria-describedby={s.used_by.length ? `in-use-${s.name}` : undefined}
                    aria-label="Delete {s.name}"
                    onclick={() => askDelete(s.name)}>Delete</button
                  >
                </div>
                {#if s.used_by.length}
                  <span class="why" id="in-use-{s.name}">
                    In use by {s.used_by.length} target{s.used_by.length === 1 ? "" : "s"}. Remove
                    the references first.
                  </span>
                {/if}
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .flush {
    padding: 0;
  }
  .check label {
    font-weight: 400;
    display: flex;
    gap: var(--space-2);
    align-items: center;
  }
  .why {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    margin-top: var(--space-1);
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
  section.banner p {
    margin: 0 0 var(--space-2);
  }
</style>
