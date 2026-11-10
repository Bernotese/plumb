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
  let ignored = $state(false);
  let allTargets = $state(false);
  let onlyChanged = $state(false);

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

  const changedAttrs = $derived([...new Set(issue?.changes.map((c) => c.attr) ?? [])]);
  const canIgnore = $derived(
    issue?.actions.includes("ignore_once") || issue?.actions.includes("ignore_rule"),
  );

  async function ignoreOnce() {
    busy = true;
    actionError = null;
    try {
      issue = await api.ignoreOnce(id, reason || undefined);
      ignored = true;
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }

  async function ignoreRule() {
    busy = true;
    actionError = null;
    try {
      const plan = await api.resolveIssue(id, "ignore_rule", reason || undefined, {
        all_targets: allTargets,
        ...(onlyChanged && changedAttrs.length ? { attrs: changedAttrs } : {}),
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      actionError = err;
    } finally {
      busy = false;
    }
  }

  async function resolve(action: Exclude<IssueAction, "ignore_once" | "ignore_rule">) {
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
    {#if ignored}
      <p class="banner ok" role="status">
        Ignored once. The same drift opens no new issue until the resource matches again or a run
        applies it.
      </p>
    {/if}
    {#if issue.resolution}
      <p>
        Resolved: <strong
          >{issue.resolution.action === "ignore" ? "ignored" : issue.resolution.action}</strong
        >
        by
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
        {#if issue.actions.includes("accept") && !hidden}Both make a plan.{:else}This makes a plan.{/if}
        Nothing changes until you approve it.
      </p>

      {#if canIgnore}
        <h3>Ignore</h3>
        {#if issue.actions.includes("ignore_once")}
          <div class="option">
            <button class="btn" type="button" disabled={busy} onclick={ignoreOnce}>
              Ignore once
            </button>
            <p class="muted">
              Resolves this issue now, without a plan. The guest keeps its value until the next
              apply. A new, different drift opens a new issue.
            </p>
          </div>
        {/if}
        {#if issue.actions.includes("ignore_rule")}
          <fieldset class="option">
            <legend>Ignore by rule</legend>
            <div class="choices" role="radiogroup" aria-label="Targets of the rule">
              <label
                ><input type="radio" name="rule-scope" value={false} bind:group={allTargets} />
                Only target <span class="mono">{issue.target}</span></label
              >
              <label
                ><input type="radio" name="rule-scope" value={true} bind:group={allTargets} />
                All targets, also future ones</label
              >
            </div>
            <div class="choices" role="radiogroup" aria-label="Attributes of the rule">
              <label
                ><input type="radio" name="rule-attrs" value={false} bind:group={onlyChanged} />
                All attributes of <span class="mono">{issue.resource}</span></label
              >
              <label
                ><input type="radio" name="rule-attrs" value={true} bind:group={onlyChanged} />
                Only the changed attributes:
                <span class="mono">{changedAttrs.join(", ")}</span></label
              >
            </div>
            <button class="btn" type="button" disabled={busy} onclick={ignoreRule}>
              Make ignore-rule plan
            </button>
            <p class="muted">
              The rule is a document of the desired state. Only a person can approve the plan. The
              next apply still writes the desired state.
            </p>
          </fieldset>
        {/if}
      {/if}
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
  h3 {
    font-size: var(--text-md);
    margin: var(--space-4) 0 var(--space-2);
  }
  .option {
    margin-bottom: var(--space-3);
  }
  .option p {
    margin: var(--space-1) 0 0;
  }
  .choices {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    margin-bottom: var(--space-3);
  }
  .choices label {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    flex-wrap: wrap;
  }
  .body {
    white-space: pre-wrap;
    margin: 0;
  }
</style>
