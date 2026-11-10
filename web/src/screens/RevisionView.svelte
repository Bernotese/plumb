<script lang="ts">
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import type { RevisionDiff } from "../lib/types";
  import DocDiff from "../ui/DocDiff.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  let diff = $state<RevisionDiff | null>(null);
  let error = $state<unknown>(null);
  let rollbackError = $state<unknown>(null);
  let nothing = $state(false);
  let reason = $state("");
  /** Set by a submit with an empty reason; the field shows an error. */
  let reasonMissing = $state(false);
  let busy = $state(false);

  $effect(() => {
    api.diffRevision(id).then(
      (d) => (diff = d),
      (e: unknown) => (error = e),
    );
  });

  const counts = $derived({
    added: diff?.changes.filter((c) => c.op === "added").length ?? 0,
    changed: diff?.changes.filter((c) => c.op === "changed").length ?? 0,
    removed: diff?.changes.filter((c) => c.op === "removed").length ?? 0,
  });

  async function rollback(e: SubmitEvent) {
    e.preventDefault();
    rollbackError = null;
    nothing = false;
    reasonMissing = !reason.trim();
    if (reasonMissing) {
      document.getElementById("rollback-reason")?.focus();
      return;
    }
    busy = true;
    try {
      const plan = await api.rollbackRevision(id, reason.trim());
      go({ name: "plan", id: plan.id });
    } catch (err) {
      if (err instanceof ApiError && err.problem.type === "urn:plumb:problem:nothing-to-do") {
        nothing = true;
      } else {
        rollbackError = err;
      }
    } finally {
      busy = false;
    }
  }
</script>

<p class="back"><a href={href({ name: "history" })}>← History</a></p>
<h1 id="page-title" tabindex="-1">Revision {id}</h1>
<ProblemBox {error} />
{#if diff}
  <p>
    {#if diff.from === null}
      Compared with the empty state.
    {:else}
      Changes from revision {diff.from} to revision {diff.to}:
    {/if}
    {counts.added} added, {counts.changed} changed, {counts.removed} removed document{diff.changes
      .length === 1
      ? ""
      : "s"}.
  </p>

  {#each diff.changes as c, i (`${c.kind}/${c.name}`)}
    <DocDiff change={c} id="doc-{i}" />
  {:else}
    <p class="muted">This revision has the same documents as the one before.</p>
  {/each}

  <form class="card" onsubmit={rollback} novalidate>
    <h2>Roll back to this revision</h2>
    <p>
      Plumb makes a plan that puts the documents back to revision {id}. Nothing changes until you
      approve the plan. Secret values do not roll back.
    </p>
    <div class="field">
      <label for="rollback-reason">Reason (required)</label>
      <textarea
        id="rollback-reason"
        bind:value={reason}
        required
        maxlength="2000"
        aria-invalid={reasonMissing}
        aria-describedby={reasonMissing ? "rollback-error rollback-hint" : "rollback-hint"}
        oninput={() => (reasonMissing = false)}></textarea>
      {#if reasonMissing}
        <p id="rollback-error" class="error">Give a reason for the rollback.</p>
      {/if}
      <p id="rollback-hint" class="muted hint">
        Why do you roll back? The reason goes into the plan.
      </p>
    </div>
    {#if nothing}
      <div class="banner info" role="status">
        <strong>Nothing to roll back.</strong> Revision {id} has the same documents as the head revision.
      </div>
    {/if}
    <ProblemBox error={rollbackError} />
    <div class="row">
      <button class="btn primary" type="submit" disabled={busy}>
        Roll back to this revision
      </button>
    </div>
  </form>
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .back {
    margin: 0 0 var(--space-2);
    font-size: var(--text-sm);
  }
  .error {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }
  .hint {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
  }
</style>
