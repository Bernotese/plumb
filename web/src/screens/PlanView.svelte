<script lang="ts">
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import type { Plan } from "../lib/types";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ChangeTable from "../ui/ChangeTable.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  let plan = $state<Plan | null>(null);
  let error = $state<unknown>(null);
  let approveError = $state<unknown>(null);
  let comment = $state("");
  let busy = $state(false);

  const load = () =>
    api.getPlan(id).then(
      (p) => (plan = p),
      (e: unknown) => (error = e),
    );
  $effect(() => {
    void load();
  });

  const count = $derived(
    plan
      ? plan.targets.flatMap((t) => t.resources).filter((r) => r.action !== "restart").length
      : 0,
  );
  const restarts = $derived(
    plan
      ? plan.targets.flatMap((t) => t.resources).filter((r) => r.action === "restart").length
      : 0,
  );
  const actionLabel = {
    create: "Create",
    update: "Change",
    delete: "Delete",
    restart: "Restart",
  } as const;

  async function approve(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    approveError = null;
    try {
      const run = await api.approvePlan(id, comment || undefined);
      go({ name: "run", id: run.id });
    } catch (err) {
      approveError = err;
      if (err instanceof ApiError && err.problem.status === 409) await load();
    } finally {
      busy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">Plan {id}</h1>
<ProblemBox {error} />
{#if plan}
  {#if plan.status === "stale"}
    <div class="banner warn" role="alert">
      <strong>This plan is stale.</strong> The desired state changed after it was made, so it cannot be
      approved. Make the change again to get a new plan.
    </div>
  {:else if plan.status === "approved"}
    <div class="banner ok">
      Approved by <span class="mono">{plan.approved_by}</span> at {formatTime(plan.approved_at)}.
      {#if plan.run_id}<a href={href({ name: "run", id: plan.run_id })}>Watch run {plan.run_id}</a
        >{/if}
    </div>
  {/if}

  <div class="card">
    <dl class="facts">
      <dt>Reason</dt>
      <dd>{plan.reason}</dd>
      <dt>Author</dt>
      <dd class="mono">{plan.author}</dd>
      <dt>Created</dt>
      <dd>{formatTime(plan.created_at)}</dd>
      <dt>Risk of the plan</dt>
      <dd><RiskBadge risk={plan.risk} /></dd>
      <dt>Summary</dt>
      <dd>
        {count} resource change{count === 1 ? "" : "s"}{restarts
          ? `, ${restarts} restart${restarts === 1 ? "" : "s"}`
          : ""}
      </dd>
      {#if plan.issue_id}
        <dt>Issue</dt>
        <dd>
          <a href={href({ name: "issue", id: plan.issue_id })}>Issue {plan.issue_id}</a>
          ({plan.origin})
        </dd>
      {/if}
    </dl>
  </div>

  {#each plan.warnings as w (w)}
    <p class="banner warn">{w}</p>
  {/each}

  {#each plan.targets as t (t.target)}
    <h2>Target <span class="mono">{t.target}</span></h2>
    {#each t.resources as r, i (i)}
      <section
        class="card resource"
        class:restart={r.action === "restart"}
        aria-labelledby="res-{t.target}-{i}"
      >
        <div class="spread">
          <h3 id="res-{t.target}-{i}">
            <Badge
              tone={r.action === "delete" ? "danger" : r.action === "restart" ? "warn" : "info"}
              label={actionLabel[r.action]}
            />
            <span class="mono">{r.resource}</span>
          </h3>
          <RiskBadge risk={r.risk} />
        </div>
        {#if r.action === "restart"}
          <p class="muted">
            {r.resource.startsWith("guest:")
              ? "The guest restarts. Its services are down for a short time."
              : "The service restarts because a resource it depends on changes."}
          </p>
        {/if}
        <ChangeTable
          changes={r.changes}
          caption="Changes of {r.resource}"
          fromLabel="Now on the guest"
          toLabel="After the apply"
        />
      </section>
    {/each}
  {/each}

  {#if plan.status === "pending"}
    <form class="card" onsubmit={approve}>
      <h2>Approve</h2>
      <div class="field">
        <label for="approve-comment">Approval comment (optional)</label>
        <textarea id="approve-comment" bind:value={comment}></textarea>
      </div>
      <ProblemBox error={approveError} />
      <div class="row">
        <button class="btn primary" type="submit" disabled={busy}>Approve and apply</button>
        <a class="btn" href={href({ name: "targets" })}>Not now</a>
      </div>
    </form>
  {:else}
    <ProblemBox error={approveError} />
  {/if}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }
  dt {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  dd {
    margin: 0;
  }
  h3 {
    font-size: var(--text-md);
    margin: 0 0 var(--space-2);
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }
  .restart {
    border-left: 4px solid var(--color-warn);
  }
</style>
