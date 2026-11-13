<script lang="ts">
  // Raw mode on the Target page (T-118, D-296): every override is a raw
  // resource. Edit its content or go back to the Block; each is a proposal.
  import type { PlumbApi } from "../lib/client";
  import { rawResources, withRaw } from "../lib/raw";
  import { go } from "../lib/router";
  import type { TargetView } from "../lib/types";
  import ProblemBox from "./ProblemBox.svelte";

  let { api, target }: { api: PlumbApi; target: TargetView } = $props();

  const rows = $derived(rawResources(target.document.spec));
  let editing = $state<string | null>(null);
  let draft = $state("");
  let reason = $state("");
  let reasonError = $state("");
  let error = $state<unknown>(null);
  let busy = $state(false);

  const slug = (id: string) => id.replace(/[^a-z0-9]+/giu, "-");

  function edit(id: string, content: string) {
    editing = id;
    draft = content;
    queueMicrotask(() => document.getElementById(`raw-${slug(id)}-content`)?.focus());
  }

  async function propose(id: string, content: string | null) {
    error = null;
    reasonError = reason.trim() ? "" : "Say why you make this change.";
    if (reasonError) {
      document.getElementById("raw-reason")?.focus();
      return;
    }
    busy = true;
    try {
      const plan = await api.createPlan({
        reason,
        put: [
          {
            kind: "Target",
            name: target.document.name,
            spec: withRaw(target.document.spec, id, content),
          },
        ],
      });
      go({ name: "plan", id: plan.id });
    } catch (e) {
      error = e;
    } finally {
      busy = false;
    }
  }
</script>

<section class="card" aria-labelledby="raw-title">
  <h2 id="raw-title">Raw resources</h2>
  <p class="muted">
    A raw resource keeps the attributes below on this Target. The Block does not set them any more.
    "Accept as raw" on an Issue makes one. "Back to the Block" removes it.
  </p>
  {#each rows as r (r.id)}
    {@const s = slug(r.id)}
    <article class="raw" aria-labelledby="raw-{s}-title">
      <h3 class="mono" id="raw-{s}-title">{r.id}</h3>
      {#if r.attrs.length}
        <dl class="facts">
          {#each r.attrs as a (a.name)}
            <dt class="label">{a.name}</dt>
            <dd class="mono">{a.value}</dd>
          {/each}
        </dl>
      {/if}
      {#if r.content !== undefined}
        {#if editing === r.id}
          <div class="field">
            <label for="raw-{s}-content">Content of <span class="mono">{r.id}</span></label>
            <textarea id="raw-{s}-content" class="mono" rows="8" bind:value={draft}></textarea>
          </div>
        {:else}
          <p class="label">content</p>
          <pre class="mono value" id="raw-{s}-value">{r.content}</pre>
        {/if}
      {/if}
      <div class="row">
        {#if editing === r.id}
          <button
            class="btn primary"
            type="button"
            disabled={busy}
            onclick={() => propose(r.id, draft)}>Show the plan</button
          >
          <button class="btn" type="button" onclick={() => (editing = null)}>Cancel</button>
        {:else}
          {#if r.content !== undefined}
            <button class="btn" type="button" onclick={() => edit(r.id, r.content ?? "")}
              >Edit content</button
            >
          {/if}
          <button class="btn" type="button" disabled={busy} onclick={() => propose(r.id, null)}
            >Back to the Block</button
          >
        {/if}
      </div>
    </article>
  {:else}
    <p class="muted" id="raw-none">This Target has no raw resources.</p>
  {/each}
  {#if rows.length}
    <div class="field">
      <label for="raw-reason">Reason<span aria-hidden="true"> *</span></label>
      <textarea
        id="raw-reason"
        bind:value={reason}
        maxlength="2000"
        aria-invalid={!!reasonError}
        aria-describedby={reasonError ? "raw-reason-error" : undefined}></textarea>
      {#if reasonError}<span class="error" id="raw-reason-error">{reasonError}</span>{/if}
    </div>
    <ProblemBox {error} />
    <p class="muted">Each button makes a plan. Nothing changes until you approve it.</p>
  {/if}
</section>

<style>
  .card h2 {
    margin-top: 0;
  }
  .raw {
    border-top: 1px solid var(--color-border);
    padding-top: var(--space-3);
    margin-bottom: var(--space-4);
  }
  .raw h3 {
    margin: 0 0 var(--space-2);
    overflow-wrap: anywhere;
  }
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .facts dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .label {
    margin: 0 0 var(--space-1);
    font-weight: 600;
    font-size: var(--text-sm);
  }
  .value {
    margin: 0 0 var(--space-3);
    padding: var(--space-2);
    background: var(--color-surface-2);
    border-radius: var(--radius);
    white-space: pre-wrap;
    max-height: 12rem;
    overflow: auto;
  }
</style>
