<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import { can } from "../lib/session.svelte";
  import type { PageOf, Plan } from "../lib/types";
  import { formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api, pageSize = 50 }: { api: PlumbApi; pageSize?: number } = $props();
  let filter = $state<Plan["status"] | "all">("pending");
  let first = $state<PageOf<Plan> | null>(null);
  let older = $state<Plan[]>([]);
  let next = $state<string | null>(null);
  let error = $state<unknown>(null);
  let moreError = $state<unknown>(null);
  let busy = $state(false);
  let dropping = $state<number | null>(null);
  let dropError = $state<unknown>(null);
  let dropBusy = $state(false);
  let announce = $state("");
  // A reload of the first page starts a new generation; an older-page answer
  // of an earlier generation is dropped (as in History).
  let generation = 0;

  const tone: Record<Plan["status"], Tone> = { pending: "warn", approved: "ok", stale: "muted" };

  $effect(() => {
    const status = filter === "all" ? undefined : filter;
    const live = liveSnapshot({
      load: () => api.listPlans(status ? { status } : undefined, undefined, pageSize),
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
        if (e.type.startsWith("plan.")) l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const items = $derived([...(first?.items ?? []), ...older]);

  async function loadMore() {
    if (!next) return;
    const gen = generation;
    const status = filter === "all" ? undefined : filter;
    busy = true;
    moreError = null;
    try {
      const p = await api.listPlans(status ? { status } : undefined, next, pageSize);
      if (gen !== generation) return;
      older = [...older, ...p.items];
      next = p.next;
    } catch (e) {
      if (gen === generation) moreError = e;
    } finally {
      if (gen === generation) busy = false;
    }
  }

  async function askDrop(id: number) {
    dropping = id;
    dropError = null;
    await tick();
    document.getElementById(`drop-confirm-${id}`)?.focus();
  }

  async function drop(id: number) {
    dropBusy = true;
    dropError = null;
    try {
      await api.discardPlan(id);
      dropping = null;
      announce = `Plan ${id} dropped.`;
      // The plan.stale event reloads the list; set it here too for a quick answer.
      const p = items.find((x) => x.id === id);
      if (p) p.status = "stale";
    } catch (e) {
      dropError = e;
    } finally {
      dropBusy = false;
    }
  }

  function targetsOf(p: Plan): string {
    const names = [...p.targets.map((t) => t.target), ...(p.images ?? []).map((i) => i.image)];
    return names.length ? names.join(", ") : "–";
  }
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Plans</h1>
  <div class="row">
    <label for="plan-filter">Show</label>
    <select id="plan-filter" bind:value={filter}>
      <option value="pending">Pending</option>
      <option value="approved">Approved</option>
      <option value="stale">Stale or dropped</option>
      <option value="all">All</option>
    </select>
  </div>
</div>
<p class="muted">
  A plan is a proposed change. Nothing happens until a person or a token approves it. Drop a pending
  plan that you do not want: it can never be applied after that.
</p>
<p class="sr-only" aria-live="polite">{announce}</p>
<ProblemBox {error} />
<ProblemBox error={dropError} />
{#if first === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if first && items.length === 0}
  <p class="muted">
    {filter === "pending" ? "No plan waits for approval." : "No plans to show."}
  </p>
{:else if first}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Plans, newest first, scrolls sideways"
  >
    <table class="stack">
      <caption class="sr-only">Plans, newest first</caption>
      <thead>
        <tr>
          <th scope="col">Plan</th>
          <th scope="col">Status</th>
          <th scope="col">Risk</th>
          <th scope="col">Reason</th>
          <th scope="col">Changes</th>
          <th scope="col">Author</th>
          <th scope="col">Created</th>
          <th scope="col">Action</th>
        </tr>
      </thead>
      <tbody>
        {#each items as p (p.id)}
          <tr>
            <th scope="row"><a href={href({ name: "plan", id: p.id })}>Plan {p.id}</a></th>
            <td data-label="Status">
              <div>
                <Badge tone={tone[p.status]} label={p.status} />
                {#if p.run_id}<a class="sub" href={href({ name: "run", id: p.run_id })}
                    >Run {p.run_id}</a
                  >{/if}
              </div>
            </td>
            <td data-label="Risk"><RiskBadge risk={p.risk} /></td>
            <td data-label="Reason" class="reason"
              >{#if p.author.startsWith("token:")}<span class="by-token"
                  ><Badge tone="warn" label="by token" /><span class="sr-only">:&nbsp;</span><span
                    class="line"
                    title={p.reason}>{p.reason}</span
                  ></span
                >{:else}{p.reason}{/if}</td
            >
            <td data-label="Changes" class="mono">{targetsOf(p)}</td>
            <td data-label="Author" class="mono">{p.author}</td>
            <td data-label="Created" class="nowrap">{formatTime(p.created_at)}</td>
            <td data-label="Action">
              {#if p.status !== "pending" || !can("discardPlan")}
                <span class="muted">–</span>
              {:else if dropping === p.id}
                <div class="confirm" role="group" aria-label="Drop plan {p.id}?">
                  <span>Drop plan {p.id}?</span>
                  <div class="row">
                    <button
                      id="drop-confirm-{p.id}"
                      class="btn danger"
                      type="button"
                      disabled={dropBusy}
                      onclick={() => drop(p.id)}>Drop</button
                    >
                    <button class="btn" type="button" onclick={() => (dropping = null)}
                      >Cancel</button
                    >
                  </div>
                </div>
              {:else}
                <button
                  class="btn"
                  type="button"
                  aria-label="Drop plan {p.id}"
                  onclick={() => askDrop(p.id)}>Drop</button
                >
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
        {busy ? "Loading…" : "Load older plans"}
      </button>
    </div>
  {/if}
{/if}

<style>
  /* A token's reason (#272 F4, review of #291): marked and cut to one line. */
  .by-token {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    min-width: 0;
  }
  .by-token .line {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
    max-width: 24rem;
  }
  /* Narrow screens (table.stack): a badge or a button keeps its size. */
  td > :global(.badge),
  td > .btn {
    justify-self: start;
  }
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
  .sub {
    display: block;
    font-size: var(--text-xs);
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }
  .more {
    margin-top: var(--space-4);
  }
</style>
