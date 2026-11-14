<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { PlumbEvent, Run } from "../lib/types";
  import { BUILD_STEP_TEXT, TRIGGER_TEXT } from "../lib/images";
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
    warning: "warn",
  };
  // D-396: a Run with warning steps never reads as a clean success.
  const runLabel = (r: { status: string; warnings?: number }) =>
    r.status === "succeeded" && (r.warnings ?? 0) > 0
      ? `succeeded, ${r.warnings} warning${r.warnings === 1 ? "" : "s"}`
      : r.status;

  $effect(() => {
    // The snapshot follows the stream without gaps (live.ts): it is loaded
    // again on every (re)open. The server adds steps while the run goes on,
    // so a step the snapshot does not have yet, and the end of the run,
    // load it again (coalesced).
    const live = liveSnapshot({
      load: () => api.getRun(id),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (r) => {
        run = r;
        if (r.status !== "running") announce = `Run ${id} ${r.status}.`;
      },
      onError: (e) => (error = e),
      onEvent: (e: PlumbEvent, l) => {
        if (e.subject !== `run:${id}` || !run) return;
        const d = e.data as {
          target?: string;
          image?: string;
          resource?: string;
          status?: string;
        };
        if (e.type === "run.step")
          announce = `${d.resource} on ${d.image ? `image ${d.image}` : d.target}: ${d.status}`;
        if (!applyStepEvent(run, e) && (e.type === "run.step" || e.type === "run.finished"))
          l.reloadSoon();
      },
    });
    return () => live.stop();
  });
</script>

<h1 id="page-title" tabindex="-1">Run {id}</h1>
<ProblemBox {error} />
<p class="sr-only" aria-live="polite">{announce}</p>
{#if run}
  <div class="card spread">
    <div>
      <span class="sr-only">Run status:</span>
      <Badge
        tone={run.status === "succeeded" && run.warnings > 0
          ? "warn"
          : (tone[run.status] ?? "muted")}
        label={runLabel(run)}
      />
      <span class="muted"
        >Started {formatTime(run.started_at)}{run.finished_at
          ? `, ended ${formatTime(run.finished_at)}`
          : ""}</span
      >
    </div>
    <a href={href({ name: "plan", id: run.plan_id })}>Plan {run.plan_id}</a>
  </div>
  {#if run.waits_for_restart > 0}
    <p class="card waits" id="run-waits">
      {run.waits_for_restart}
      {run.waits_for_restart === 1 ? "change waits" : "changes wait"} for a restart of the VM: set in
      Proxmox, not active until the next stop and start. See {#each run.targets as t, i (t.target)}{i
          ? ", "
          : ""}<a href={href({ name: "target-edit", target: t.target })}>{t.target}</a>{/each}.
    </p>
  {/if}
  {#each run.targets as t (t.target)}
    <h2>
      Target <span class="mono">{t.target}</span>
      <Badge tone={tone[t.status] ?? "muted"} label={t.status} />
    </h2>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      class="table-wrap card flush"
      tabindex="0"
      role="region"
      aria-label="Steps on {t.target}, scrolls sideways"
    >
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
              <td>
                <Badge tone={tone[s.status] ?? "muted"} label={s.status} />
                {#if s.rolled_back}<span class="rolled">rolled back</span>{/if}
              </td>
              <td>
                {#if s.rolled_back}<p class="rolled-text">
                    The agent lost the link to Plumb after the change and put the old state back.
                    The guest is as before the Run.
                  </p>{/if}
                <pre class="mono out">{s.output || "–"}</pre>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/each}
  {#each run.images ?? [] as b (b.image)}
    <h2>
      Image <a class="mono" href={href({ name: "image", image: b.image })}>{b.image}</a>
      <span class="muted">version {b.version} · {TRIGGER_TEXT[b.trigger]}</span>
      <Badge tone={tone[b.status] ?? "muted"} label={b.status} />
    </h2>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      class="table-wrap card flush"
      tabindex="0"
      role="region"
      aria-label="Build steps of image {b.image} version {b.version}, scrolls sideways"
    >
      <table>
        <caption class="sr-only">Build steps of image {b.image} version {b.version}</caption>
        <thead>
          <tr>
            <th scope="col">Step</th>
            <th scope="col">Action</th>
            <th scope="col">Status</th>
            <th scope="col">Output</th>
          </tr>
        </thead>
        <tbody>
          {#each b.steps as s (s.resource)}
            <tr>
              <th scope="row"
                ><span class="mono">{s.resource}</span>{#if BUILD_STEP_TEXT[s.resource]}<span
                    class="step-text">{BUILD_STEP_TEXT[s.resource]}</span
                  >{/if}</th
              >
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
  .rolled {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-danger);
  }
  .rolled-text {
    margin: 0 0 var(--space-1);
  }
  .waits {
    border-left: 4px solid var(--color-warn);
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .out {
    margin: 0;
    white-space: pre-wrap;
  }
  .step-text {
    display: block;
    font-weight: normal;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  h2 {
    flex-wrap: wrap;
    display: flex;
    gap: var(--space-2);
    align-items: center;
  }
</style>
