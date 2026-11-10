<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { TargetView } from "../lib/types";
  import { formatTime, targetState } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let targets = $state<TargetView[] | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    // The list follows the event stream (live.ts): reload on every
    // (re)open and, coalesced, on status, run and issue events.
    const live = liveSnapshot({
      load: () => api.listTargets(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (t) =>
        (targets = t.sort((a, b) => a.document.name.localeCompare(b.document.name))),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "target.status" || e.type === "run.finished" || e.type.startsWith("issue."))
          l.reloadSoon();
      },
    });
    return () => live.stop();
  });
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Targets</h1>
  <a class="btn primary" href={href({ name: "target-new" })}>New target</a>
</div>
<ProblemBox {error} />
{#if targets === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if targets && targets.length === 0}
  <p class="muted">No targets yet. Create the first one.</p>
{:else if targets}
  <div class="table-wrap card flush">
    <table>
      <caption class="sr-only">Targets and their status</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Status</th>
          <th scope="col">Image</th>
          <th scope="col">Size</th>
          <th scope="col">Address</th>
          <th scope="col">Last seen</th>
        </tr>
      </thead>
      <tbody>
        {#each targets as t (t.document.name)}
          {@const st = targetState(t)}
          {@const g = t.document.spec.guest}
          <tr>
            <th scope="row">
              <a class="mono" href={href({ name: "target-edit", target: t.document.name })}
                >{t.document.name}</a
              >
            </th>
            <td
              ><Badge tone={st.tone} label={st.label} title={st.detail} /><span class="sr-only"
                >: {st.detail}</span
              >{#if t.status.ignored_drift?.length}<span class="ignored"
                  >{t.status.ignored_drift.length} ignored by rule</span
                >{/if}</td
            >
            <td class="mono">{t.document.spec.image}</td>
            <td class="nowrap">{g.cores} CPU · {g.memory / 1024} GiB · {g.disk} GiB</td>
            <td class="mono">{t.status.ips?.join(", ") || "–"}</td>
            <td class="nowrap">{formatTime(t.status.last_seen)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .ignored {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
</style>
