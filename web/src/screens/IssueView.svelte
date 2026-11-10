<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import type { Issue, IssueAction } from "../lib/types";
  import { formatTime, isSensitive } from "../lib/view";
  import ChangeTable from "../ui/ChangeTable.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  let issue = $state<Issue | null>(null);
  let error = $state<unknown>(null);
  let actionError = $state<unknown>(null);
  let reason = $state("");
  let busy = $state(false);

  $effect(() => {
    api.getIssue(id).then(
      (i) => (issue = i),
      (e: unknown) => (error = e),
    );
  });

  const hidden = $derived(issue?.changes.some(isSensitive) ?? false);
  const source = $derived(
    issue?.resource === "guest:config" ? "Proxmox" : "the agent in the guest",
  );

  async function resolve(action: IssueAction) {
    busy = true;
    actionError = null;
    try {
      const plan = await api.resolveIssue(id, action, reason || undefined);
      go({ name: "plan", id: plan.id });
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">Issue {id}</h1>
<ProblemBox {error} />
{#if issue}
  <div class="card">
    <p>
      <span class="mono">{issue.resource}</span> on <span class="mono">{issue.target}</span> differs
      from the desired state. Found by {source} at {formatTime(issue.opened_at)}.
    </p>
    <p>Risk of a revert: <RiskBadge risk={issue.risk} /> · Status: {issue.status}</p>
    {#if issue.resolution}
      <p>
        Resolved: <strong>{issue.resolution.action}</strong> by
        <span class="mono">{issue.resolution.by}</span> at {formatTime(issue.resolution.at)}.
      </p>
    {/if}
    {#if issue.plan_id}
      <p>Plan: <a href={href({ name: "plan", id: issue.plan_id })}>Plan {issue.plan_id}</a></p>
    {/if}
  </div>

  <h2>Difference</h2>
  <ChangeTable
    changes={issue.changes}
    caption="Difference of {issue.resource}"
    fromLabel={issue.status === "open" ? "On the guest now" : "On the guest when found"}
    toLabel="Desired"
  />
  {#if hidden}
    <p class="muted">
      A secret value changed. Plumb never shows it, and "Accept" is not offered for it.
    </p>
  {/if}

  {#if issue.status === "open"}
    <section class="card" aria-labelledby="resolve-title">
      <h2 id="resolve-title">Resolve</h2>
      <div class="field">
        <label for="resolve-reason">Reason (optional)</label>
        <textarea id="resolve-reason" bind:value={reason}></textarea>
      </div>
      <ProblemBox error={actionError} />
      <div class="row">
        {#if issue.actions.includes("revert")}
          <button
            class="btn primary"
            type="button"
            disabled={busy}
            onclick={() => resolve("revert")}
          >
            Revert: apply the desired state
          </button>
        {/if}
        {#if issue.actions.includes("accept") && !hidden}
          <button class="btn" type="button" disabled={busy} onclick={() => resolve("accept")}>
            Accept: keep the guest value
          </button>
        {/if}
      </div>
      <p class="muted">
        {issue.actions.length > 1 ? "Both make a plan." : "This makes a plan."} Nothing changes until
        you approve it.
      </p>
    </section>
  {/if}

  {#if issue.comments.length}
    <h2>Comments</h2>
    {#each issue.comments as c (c.id)}
      <div class="card">
        <p class="muted"><span class="mono">{c.author}</span> · {formatTime(c.created_at)}</p>
        <p class="body">{c.body}</p>
      </div>
    {/each}
  {/if}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .body {
    white-space: pre-wrap;
    margin: 0;
  }
</style>
