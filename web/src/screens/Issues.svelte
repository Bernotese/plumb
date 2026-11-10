<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { Issue } from "../lib/types";
  import { formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api }: { api: PlumbApi } = $props();
  let issues = $state<Issue[] | null>(null);
  let error = $state<unknown>(null);
  let filter = $state<"open" | "resolving" | "resolved" | "all">("open");
  const tone: Record<Issue["status"], Tone> = { open: "warn", resolving: "info", resolved: "ok" };

  $effect(() => {
    // The inbox follows the event stream (live.ts): reload on every
    // (re)open and, coalesced, on every issue event.
    const live = liveSnapshot({
      load: () => api.listIssues(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (i) => (issues = i),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type.startsWith("issue.")) l.reloadSoon();
      },
    });
    return () => live.stop();
  });
  const shown = $derived(issues?.filter((i) => filter === "all" || i.status === filter) ?? []);
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Issues</h1>
  <div class="row">
    <label for="issue-filter">Show</label>
    <select id="issue-filter" bind:value={filter}>
      <option value="open">Open</option>
      <option value="resolving">Resolving</option>
      <option value="resolved">Resolved</option>
      <option value="all">All</option>
    </select>
  </div>
</div>
<ProblemBox {error} />
{#if issues === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if shown.length === 0}
  <p class="muted">
    No {filter === "all" ? "" : filter} issues. Every managed resource matches its desired state.
  </p>
{:else}
  <div class="table-wrap card flush">
    <table>
      <caption class="sr-only">Drift issues</caption>
      <thead>
        <tr>
          <th scope="col">Issue</th>
          <th scope="col">Target</th>
          <th scope="col">Resource</th>
          <th scope="col">Status</th>
          <th scope="col">Revert risk</th>
          <th scope="col">Opened</th>
        </tr>
      </thead>
      <tbody>
        {#each shown as i (i.id)}
          <tr>
            <th scope="row"><a href={href({ name: "issue", id: i.id })}>Issue {i.id}</a></th>
            <td class="mono">{i.target}</td>
            <td class="mono">{i.resource}</td>
            <td><Badge tone={tone[i.status]} label={i.status} /></td>
            <td><RiskBadge risk={i.risk} /></td>
            <td>{formatTime(i.opened_at)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .flush {
    padding: 0;
    overflow: hidden;
  }
</style>
