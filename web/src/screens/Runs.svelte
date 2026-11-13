<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { PageOf, Run } from "../lib/types";
  import { formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, pageSize = 50 }: { api: PlumbApi; pageSize?: number } = $props();
  let first = $state<PageOf<Run> | null>(null);
  let older = $state<Run[]>([]);
  let next = $state<string | null>(null);
  let error = $state<unknown>(null);
  let moreError = $state<unknown>(null);
  let busy = $state(false);
  let generation = 0;

  const tone: Record<Run["status"], Tone> = { running: "info", succeeded: "ok", failed: "danger" };

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listRuns(undefined, pageSize),
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
        if (e.type === "run.started" || e.type === "run.finished") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const items = $derived([...(first?.items ?? []), ...older]);

  async function loadMore() {
    if (!next) return;
    const gen = generation;
    busy = true;
    moreError = null;
    try {
      const p = await api.listRuns(next, pageSize);
      if (gen !== generation) return;
      older = [...older, ...p.items];
      next = p.next;
    } catch (e) {
      if (gen === generation) moreError = e;
    } finally {
      if (gen === generation) busy = false;
    }
  }

  /** Targets and Images of a run, with the failed ones first. */
  function partsOf(r: Run): { name: string; failed: boolean }[] {
    return [
      ...r.targets.map((t) => ({ name: t.target, failed: t.status === "failed" })),
      ...(r.images ?? []).map((i) => ({ name: i.image, failed: i.status === "failed" })),
    ].sort((a, b) => Number(b.failed) - Number(a.failed));
  }
</script>

<h1 id="page-title" tabindex="-1">Runs</h1>
<p class="muted">A run applies an approved plan. Open one to see the result per resource.</p>
<ProblemBox {error} />
{#if first === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if first && items.length === 0}
  <p class="muted">No runs yet. A run starts when a plan is approved.</p>
{:else if first}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Runs, newest first, scrolls sideways"
  >
    <table class="stack">
      <caption class="sr-only">Runs, newest first</caption>
      <thead>
        <tr>
          <th scope="col">Run</th>
          <th scope="col">Status</th>
          <th scope="col">Plan</th>
          <th scope="col">Revision</th>
          <th scope="col">Targets and images</th>
          <th scope="col">Started</th>
          <th scope="col">Ended</th>
        </tr>
      </thead>
      <tbody>
        {#each items as r (r.id)}
          <tr>
            <th scope="row"><a href={href({ name: "run", id: r.id })}>Run {r.id}</a></th>
            <td data-label="Status">
              <div>
                <Badge tone={tone[r.status]} label={r.status} />
                {#if r.waits_for_restart > 0}<span class="sub"
                    >{r.waits_for_restart} wait{r.waits_for_restart === 1 ? "s" : ""} for a restart</span
                  >{/if}
              </div>
            </td>
            <td data-label="Plan"
              ><a href={href({ name: "plan", id: r.plan_id })}>Plan {r.plan_id}</a></td
            >
            <td data-label="Revision">
              {#if r.revision}<a href={href({ name: "revision", id: r.revision })}
                  >Revision {r.revision}</a
                >{:else}<span class="muted">–</span>{/if}
            </td>
            <td data-label="Targets and images" class="mono">
              <span
                >{#each partsOf(r) as part, i (part.name)}{i ? ", " : ""}<span
                    class:failed={part.failed}>{part.name}{part.failed ? " (failed)" : ""}</span
                  >{:else}–{/each}</span
              >
            </td>
            <td data-label="Started" class="nowrap">{formatTime(r.started_at)}</td>
            <td data-label="Ended" class="nowrap">{formatTime(r.finished_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <ProblemBox error={moreError} />
  {#if next}
    <div class="row more">
      <button class="btn" type="button" disabled={busy} onclick={loadMore}>
        {busy ? "Loading…" : "Load older runs"}
      </button>
    </div>
  {/if}
{/if}

<style>
  /* Narrow screens (table.stack): a badge or a button keeps its size. */
  td > :global(.badge) {
    justify-self: start;
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .nowrap {
    white-space: nowrap;
  }
  .sub {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  .failed {
    color: var(--color-danger);
  }
  .more {
    margin-top: var(--space-4);
  }
</style>
