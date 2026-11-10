<script lang="ts">
  // A secret parameter (D-088): choose an existing secret or create one in
  // place. The form keeps only the name; the value goes straight to
  // PUT /secrets/{name} and is cleared right after.
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import type { SecretInfo } from "../lib/types";
  import ProblemBox from "./ProblemBox.svelte";

  let {
    api,
    id,
    value = $bindable(),
    required = false,
    invalid = false,
    describedby,
  }: {
    api: PlumbApi;
    id: string;
    value: string;
    required?: boolean;
    invalid?: boolean;
    describedby?: string | undefined;
  } = $props();

  let secrets = $state<SecretInfo[] | null>(null);
  let loadError = $state<unknown>(null);
  let creating = $state(false);
  let newName = $state("");
  let newValue = $state("");
  let createError = $state<unknown>(null);
  let busy = $state(false);

  const NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;
  const load = () =>
    api.listSecrets().then(
      (s) => (secrets = s),
      (e: unknown) => (loadError = e),
    );
  $effect(() => {
    void load();
  });

  // A stored name that is not in the list (deleted or not visible) stays selectable.
  const names = $derived.by(() => {
    const n = (secrets ?? []).map((s) => s.name);
    return value && !n.includes(value) ? [value, ...n] : n;
  });

  async function open() {
    creating = true;
    createError = null;
    await tick();
    document.getElementById(`${id}-new-name`)?.focus();
  }

  function close() {
    creating = false;
    newName = "";
    newValue = "";
    createError = null;
    document.getElementById(id)?.focus();
  }

  async function create() {
    if (!NAME.test(newName) || newValue === "") return;
    busy = true;
    createError = null;
    const sent = newValue;
    newValue = ""; // never kept after the request
    try {
      const info = await api.putSecret(newName, sent);
      await load();
      value = info.name;
      close();
    } catch (err) {
      createError = err;
    } finally {
      busy = false;
    }
  }

  // Enter inside the inline fields must not submit the target form.
  function onKey(e: KeyboardEvent) {
    if (e.key === "Enter") {
      e.preventDefault();
      void create();
    }
  }
</script>

<div class="picker">
  <div class="row">
    <select
      {id}
      bind:value
      {required}
      aria-invalid={invalid}
      aria-describedby={describedby}
      class="grow"
    >
      <option value="">{secrets === null && !loadError ? "Loading…" : "(choose a secret)"}</option>
      {#each names as n (n)}<option value={n}>{n}</option>{/each}
    </select>
    {#if !creating}
      <button class="btn" type="button" onclick={open}>New secret</button>
    {/if}
  </div>
  <ProblemBox error={loadError} />
  {#if creating}
    <div class="new" role="group" aria-labelledby="{id}-new-title">
      <p id="{id}-new-title" class="title">New secret</p>
      <div class="field">
        <label for="{id}-new-name">Secret name</label>
        <input
          id="{id}-new-name"
          type="text"
          class="mono"
          bind:value={newName}
          autocomplete="off"
          spellcheck="false"
          maxlength="63"
          aria-invalid={newName !== "" && !NAME.test(newName)}
          aria-describedby="{id}-new-name-hint"
          onkeydown={onKey}
        />
        <span class="hint" id="{id}-new-name-hint"
          >Lower-case letters, digits, dots and dashes.</span
        >
      </div>
      <div class="field">
        <label for="{id}-new-value">Secret value</label>
        <input
          id="{id}-new-value"
          type="password"
          bind:value={newValue}
          autocomplete="new-password"
          spellcheck="false"
          aria-describedby="{id}-new-value-hint"
          onkeydown={onKey}
        />
        <span class="hint" id="{id}-new-value-hint"
          >Plumb stores it encrypted and never shows it again. The target refers to it by name.</span
        >
      </div>
      <ProblemBox error={createError} />
      <div class="row">
        <button
          class="btn primary"
          type="button"
          disabled={busy || !NAME.test(newName) || newValue === ""}
          onclick={create}>Create secret</button
        >
        <button class="btn" type="button" onclick={close}>Cancel</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .grow {
    flex: 1 1 12rem;
    min-width: 0;
  }
  .new {
    margin-top: var(--space-2);
    padding: var(--space-3);
    border: 1px dashed var(--color-border);
    border-radius: var(--radius);
    background: var(--color-surface-2);
  }
  .title {
    margin: 0 0 var(--space-2);
    font-weight: 600;
    font-size: var(--text-sm);
  }
  .new .field {
    margin-bottom: var(--space-2);
  }
</style>
