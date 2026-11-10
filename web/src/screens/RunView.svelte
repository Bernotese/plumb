<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { href } from "../lib/router";
  import type { PlumbEvent, Run } from "../lib/types";
  import { applyStepEvent, formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  let run = $state<Run | null>(null);
  let error = $state<unknown>(null);
  let announce = $state("");

  const tone: Record<string, Tone> = {
    pending: "muted",
    running: "info",
    unchanged: "muted",
    applied: "ok",
    succeeded: "ok",
    failed: "danger",
    refused: "danger",
    skipped: "muted",
  };

  // The server adds steps while the run goes on: a step that the snapshot
  // does not have yet loads the run again (at most every 300 ms).
  let reloadTimer: ReturnType<typeof setTimeout> | undefined;
  function reloadSoon() {
    if (reloadTimer) return;
    reloadTimer = setTimeout(() => {
      reloadTimer = undefined;
      void api.getRun(id).then((r) => (run = r));
    }, 300);
  }

  function onEvent(e: PlumbEvent) {
    if (e.subject !== `run:${id}` || !run) return;
    const d = e.data as { target?: string; resource?: string; status?: string };
    if (applyStepEvent(run, e)) {
      announce = `${d.resource} on ${d.target}: ${d.status}`;
    } else if (e.type === "run.step") {
      announce = `${d.resource} on ${d.target}: ${d.status}`;
      reloadSoon();
    } else if (e.type === "run.finished") {
      void api.getRun(id).then((r) => {
        run = r;
        announce = `Run ${id} ${r.status}.`;
      });
    }
  }

  $effect(() => {
    // Subscribe first and buffer, so no step event is lost while the
    // snapshot loads; then apply the buffer to the snapshot.
    let buffer: PlumbEvent[] | null = [];
    const stop = api.events(undefined, (e: PlumbEvent) => (buffer ? buffer.push(e) : onEvent(e)));
    api.getRun(id).then(
      (r) => {
        run = r;
        const queued = buffer ?? [];
        buffer = null;
        queued.forEach(onEvent);
      },
      (e: unknown) => (error = e),
    );
    return () => {
      stop();
      clearTimeout(reloadTimer);
    };
  });
</script>

<h1 id="page-title" tabindex="-1">Run {id}</h1>
<ProblemBox {error} />
<p class="sr-only" aria-live="polite">{announce}</p>
{#if run}
  <div class="card spread">
    <div>
      <Badge tone={tone[run.status] ?? "muted"} label={run.status} />
      <span class="muted"
        >Started {formatTime(run.started_at)}{run.finished_at
          ? `, ended ${formatTime(run.finished_at)}`
          : ""}</span
      >
    </div>
    <a href={href({ name: "plan", id: run.plan_id })}>Plan {run.plan_id}</a>
  </div>
  {#each run.targets as t (t.target)}
    <h2>
      Target <span class="mono">{t.target}</span>
      <Badge tone={tone[t.status] ?? "muted"} label={t.status} />
    </h2>
    <div class="table-wrap card flush">
      <table>
        <caption class="sr-only">Steps on {t.target}</caption>
        <thead>
          <tr>
            <th scope="col">Resource</th>
            <th scope="col">Action</th>
            <th scope="col">Status</th>
            <th scope="col">Output</th>
          </tr>
        </thead>
        <tbody>
          {#each t.steps as s (s.resource + s.action)}
            <tr>
              <th scope="row" class="mono">{s.resource}</th>
              <td>{s.action ?? "–"}</td>
              <td><Badge tone={tone[s.status] ?? "muted"} label={s.status} /></td>
              <td><pre class="mono out">{s.output || "–"}</pre></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/each}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .out {
    margin: 0;
    white-space: pre-wrap;
  }
  h2 {
    display: flex;
    gap: var(--space-2);
    align-items: center;
  }
</style>
