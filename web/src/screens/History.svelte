<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { PageOf, RevisionInfo } from "../lib/types";
  import { formatTime } from "../lib/view";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, pageSize = 50 }: { api: PlumbApi; pageSize?: number } = $props();
  let first = $state<PageOf<RevisionInfo> | null>(null);
  let older = $state<RevisionInfo[]>([]);
  let next = $state<string | null>(null);
  let error = $state<unknown>(null);
  let moreError = $state<unknown>(null);
  let busy = $state(false);
  // A reload of the first page starts a new generation. An older-page answer
  // from an earlier generation is dropped, else it leaves a gap (review #1).
  let generation = 0;

  $effect(() => {
    // The first page follows the stream: a new revision reloads it, and
    // the older pages loaded by hand start again from there.
    const live = liveSnapshot({
      load: () => api.listRevisions(undefined, pageSize),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (p) => {
        generation++;
        busy = false;
        first = p;
        older = [];
        next = p.next;
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const items = $derived([...(first?.items ?? []), ...older]);
  const headId = $derived(first?.items[0]?.id);

  async function loadMore() {
    if (!next) return;
    const gen = generation;
    busy = true;
    moreError = null;
    try {
      const p = await api.listRevisions(next, pageSize);
      if (gen !== generation) return;
      older = [...older, ...p.items];
      next = p.next;
    } catch (e) {
      if (gen === generation) moreError = e;
    } finally {
      if (gen === generation) busy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">History</h1>
<p class="muted">
  Every change of the desired state is a revision. Open one to see what changed and to roll back to
  it.
</p>
<ProblemBox {error} />
{#if first === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if first && items.length === 0}
  <p class="muted">No revisions yet.</p>
{:else if first}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Revisions, newest first, scrolls sideways"
  >
    <table>
      <caption class="sr-only">Revisions, newest first</caption>
      <thead>
        <tr>
          <th scope="col">Revision</th>
          <th scope="col">Time</th>
          <th scope="col">Author</th>
          <th scope="col">Reason</th>
          <th scope="col">Plan</th>
        </tr>
      </thead>
      <tbody>
        {#each items as r (r.id)}
          <tr>
            <th scope="row">
              <a href={href({ name: "revision", id: r.id })}>Revision {r.id}</a>
              {#if r.id === headId}<span class="head">(head)</span>{/if}
            </th>
            <td class="nowrap" data-label="Time">{formatTime(r.created_at)}</td>
            <td class="mono" data-label="Author">{r.author}</td>
            <td class="reason" data-label="Reason">{r.reason}</td>
            <td class="nowrap" data-label="Plan">
              {#if r.plan_id}
                <a href={href({ name: "plan", id: r.plan_id })}>Plan {r.plan_id}</a>
              {:else}
                <span class="muted">Seeded</span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <ProblemBox error={moreError} />
  {#if next}
    <div class="row more">
      <button class="btn" type="button" disabled={busy} onclick={loadMore}>
        {busy ? "Loading…" : "Load older revisions"}
      </button>
    </div>
  {/if}
{/if}

<style>
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .nowrap {
    white-space: nowrap;
  }
  .reason {
    min-width: 12rem;
  }
  .head {
    font-weight: 400;
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  .more {
    margin-top: var(--space-4);
  }
  /* Narrow screens: one block per revision, labels from data-label. */
  @media (max-width: 40rem) {
    thead {
      position: absolute;
      left: -9999px;
    }
    tr {
      display: block;
      padding: var(--space-2) var(--space-3);
      border-bottom: 1px solid var(--color-border);
    }
    tbody th,
    tbody td {
      display: grid;
      grid-template-columns: 5rem 1fr;
      gap: var(--space-2);
      border: 0;
      padding: var(--space-1) 0;
      white-space: normal;
      min-width: 0;
    }
    tbody th {
      display: block;
    }
    tbody td::before {
      content: attr(data-label);
      color: var(--color-text-muted);
      font-family: var(--font-sans);
      font-size: var(--text-sm);
    }
  }
</style>
