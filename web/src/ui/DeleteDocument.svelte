<script lang="ts">
  // Delete a Target, Block or Image by a normal proposal (T-153, D-338): the
  // UI never deletes directly. The plan preview shows what goes and its risk.
  import { tick, type Snippet } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { go } from "../lib/router";
  import { can } from "../lib/session.svelte";
  import ProblemBox from "./ProblemBox.svelte";

  let {
    api,
    kind,
    name,
    blocked,
    children,
  }: {
    api: PlumbApi;
    kind: "Target" | "Block" | "Image";
    name: string;
    /** Why a delete cannot work now (for example "used by web1"); hides the button. */
    blocked?: string;
    /** What the delete does, shown before the button. */
    children?: Snippet;
  } = $props();

  const word = $derived(kind.toLowerCase());
  const fieldId = $derived(`delete-${word}-reason`);
  let open = $state(false);
  let reason = $state("");
  let reasonMissing = $state(false);
  let busy = $state(false);
  let error = $state<unknown>(null);

  async function ask() {
    open = true;
    reason = "";
    reasonMissing = false;
    error = null;
    await tick();
    document.getElementById(fieldId)?.focus();
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    reasonMissing = !reason.trim();
    if (reasonMissing) {
      document.getElementById(fieldId)?.focus();
      return;
    }
    busy = true;
    error = null;
    try {
      const plan = await api.createPlan({ reason: reason.trim(), delete: [{ kind, name }] });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      error = err;
    } finally {
      busy = false;
    }
  }
</script>

{#if can("createPlan")}<section class="card danger-zone" aria-labelledby="delete-{word}-title">
    <h2 id="delete-{word}-title">Delete this {word}</h2>
    {#if blocked}
      <p class="muted" id="delete-{word}-blocked">{blocked}</p>
    {:else if !open}
      {@render children?.()}
      <p class="muted">Plumb makes a plan first. Nothing is deleted until you approve it.</p>
      <div class="row">
        <button class="btn danger" type="button" onclick={ask}>Delete {word}…</button>
      </div>
    {:else}
      {@render children?.()}
      <form onsubmit={submit} novalidate>
        <div class="field">
          <label for={fieldId}>Reason (required)</label>
          <input
            id={fieldId}
            type="text"
            bind:value={reason}
            maxlength="2000"
            autocomplete="off"
            aria-invalid={reasonMissing}
            aria-describedby={reasonMissing ? `${fieldId}-error` : undefined}
            oninput={() => (reasonMissing = false)}
          />
          {#if reasonMissing}<p class="error" id="{fieldId}-error">
              Give a reason for the delete.
            </p>{/if}
        </div>
        <ProblemBox {error} />
        <div class="row">
          <button class="btn danger" type="submit" disabled={busy}>Make delete plan</button>
          <button class="btn" type="button" onclick={() => (open = false)}>Cancel</button>
        </div>
      </form>
    {/if}
  </section>{/if}

<style>
  .danger-zone {
    border-left: 4px solid var(--color-danger);
    margin-top: var(--space-6);
  }
  h2 {
    margin-top: 0;
  }
  .error {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }
</style>
