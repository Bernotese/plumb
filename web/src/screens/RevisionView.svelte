<script lang="ts">
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { can } from "../lib/session.svelte";
  import { go, href } from "../lib/router";
  import type { RevisionDiff } from "../lib/types";
  import DocDiff from "../ui/DocDiff.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, id, from }: { api: PlumbApi; id: number; from?: number } = $props();
  let diff = $state<RevisionDiff | null>(null);
  let error = $state<unknown>(null);
  let rollbackError = $state<unknown>(null);
  let nothing = $state(false);
  let reason = $state("");
  /** Set by a submit with an empty reason; the field shows an error. */
  let reasonMissing = $state(false);
  let busy = $state(false);

  $effect(() => {
    api.diffRevision(id, from).then(
      (d) => (diff = d),
      (e: unknown) => (error = e),
    );
  });

  // Compare any two revisions (T-153): GET /revisions/{n}/diff?from=.
  let head = $state<number | null>(null);
  $effect(() => {
    api.listRevisions(undefined, 1).then(
      (p) => (head = p.items[0]?.id ?? null),
      () => (head = null),
    );
  });
  // The screen is keyed by id and from (App.svelte): the start value is enough.
  // svelte-ignore state_referenced_locally
  let base = $state(from ? String(from) : "");
  let baseError = $state("");
  function compare(e: SubmitEvent) {
    e.preventDefault();
    const n = Number(base.trim());
    if (!/^[1-9][0-9]*$/.test(base.trim()) || (head !== null && n > head)) {
      baseError = `Give a revision number from 1${head !== null ? ` to ${head}` : ""}.`;
      document.getElementById("compare-from")?.focus();
      return;
    }
    if (n === id) {
      baseError = "Pick another revision than this one.";
      document.getElementById("compare-from")?.focus();
      return;
    }
    baseError = "";
    go({ name: "revision", id, from: n });
  }

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
  <form class="card compare" onsubmit={compare} novalidate aria-labelledby="compare-title">
    <h2 id="compare-title">Compare</h2>
    <div class="row">
      <div class="field">
        <label for="compare-from">Base revision</label>
        <input
          id="compare-from"
          type="text"
          inputmode="numeric"
          autocomplete="off"
          bind:value={base}
          placeholder={id > 1 ? String(id - 1) : ""}
          aria-invalid={baseError !== ""}
          aria-describedby={baseError ? "compare-error" : undefined}
          oninput={() => (baseError = "")}
        />
      </div>
      <button class="btn" type="submit">Compare</button>
    </div>
    {#if baseError}<p id="compare-error" class="error">{baseError}</p>{/if}
    <p class="muted links">
      {#if from !== undefined}<a href={href({ name: "revision", id })}>With the revision before</a
        >{/if}
      {#if head !== null && head !== id && from !== head}<a
          href={href({ name: "revision", id, from: head })}>With the head (revision {head})</a
        >{/if}
    </p>
  </form>
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
    <p class="muted">
      {from === undefined || diff.from === null
        ? "This revision has the same documents as the one before."
        : `Revision ${id} has the same documents as revision ${diff.from}.`}
    </p>
  {/each}

  {#if can("rollbackRevision")}<form class="card" onsubmit={rollback} novalidate>
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
    </form>{/if}
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
  .compare .row {
    align-items: flex-end;
  }
  .compare .field {
    margin: 0;
  }
  .compare input {
    width: 8rem;
  }
  .links {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4);
    margin: var(--space-2) 0 0;
  }
  .hint {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
  }
</style>
