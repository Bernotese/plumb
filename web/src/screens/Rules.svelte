<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { can } from "../lib/session.svelte";
  import { liveSnapshot } from "../lib/live";
  import { go, href } from "../lib/router";
  import type { RuleView } from "../lib/types";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let rules = $state<RuleView[] | null>(null);
  let error = $state<unknown>(null);
  let removing = $state<string | null>(null);
  let reason = $state("");
  let removeError = $state<unknown>(null);
  let busy = $state(false);
  let reasonMissing = $state(false);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listRules(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (r) => (rules = r),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  async function askRemove(name: string) {
    removing = name;
    reason = "";
    reasonMissing = false;
    removeError = null;
    await tick();
    document.getElementById(`remove-reason-${name}`)?.focus();
  }

  async function remove(e: SubmitEvent, name: string) {
    e.preventDefault();
    reasonMissing = !reason.trim();
    if (reasonMissing) {
      document.getElementById(`remove-reason-${name}`)?.focus();
      return;
    }
    busy = true;
    removeError = null;
    try {
      const plan = await api.createPlan({
        reason: reason.trim(),
        delete: [{ kind: "Rule", name }],
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      removeError = err;
    } finally {
      busy = false;
    }
  }
</script>

<p class="back"><a href={href({ name: "issues" })}>← Issues</a></p>
<h1 id="page-title" tabindex="-1">Ignore rules</h1>
<p class="muted">
  A rule hides matching drift: it opens no issue. The next apply still writes the desired state.
  Only a person can approve a plan that adds, changes or removes a rule.
</p>
<ProblemBox {error} />
{#if rules === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if rules && rules.length === 0}
  <p class="muted">No rules. Every drift opens an issue.</p>
{:else if rules}
  <div class="table-wrap card flush">
    <table class="stack">
      <caption class="sr-only">Ignore rules</caption>
      <thead>
        <tr>
          <th scope="col">Rule</th>
          <th scope="col">Target</th>
          <th scope="col">Resource</th>
          <th scope="col">Attributes</th>
          <th scope="col">Revision</th>
          <th scope="col">Action</th>
        </tr>
      </thead>
      <tbody>
        {#each rules as r (r.document.name)}
          {@const spec = r.document.spec}
          <tr>
            <th scope="row" class="mono">{r.document.name}</th>
            <td data-label="Target">
              {#if spec.target === "*"}All targets{:else}<span class="mono">{spec.target}</span
                >{/if}
            </td>
            <td data-label="Resource" class="mono">{spec.resource}</td>
            <td data-label="Attributes">
              {#if spec.attrs?.length}<span class="mono">{spec.attrs.join(", ")}</span
                >{:else}All{/if}
            </td>
            <td data-label="Revision">
              <a href={href({ name: "revision", id: r.revision })}>Revision {r.revision}</a>
            </td>
            <td data-label="Action">
              {#if removing === r.document.name}
                <form class="remove" onsubmit={(e) => remove(e, r.document.name)} novalidate>
                  <label for="remove-reason-{r.document.name}">Reason (required)</label>
                  <input
                    id="remove-reason-{r.document.name}"
                    type="text"
                    bind:value={reason}
                    maxlength="2000"
                    autocomplete="off"
                    aria-invalid={reasonMissing}
                    aria-describedby={reasonMissing
                      ? `remove-reason-${r.document.name}-error`
                      : undefined}
                    oninput={() => (reasonMissing = false)}
                  />
                  {#if reasonMissing}<span class="error" id="remove-reason-{r.document.name}-error"
                      >Give a reason for the removal.</span
                    >{/if}
                  <div class="row">
                    <button class="btn danger" type="submit" disabled={busy}>
                      Make removal plan
                    </button>
                    <button class="btn" type="button" onclick={() => (removing = null)}
                      >Cancel</button
                    >
                  </div>
                </form>
              {:else if can("createPlan")}
                <button
                  class="btn"
                  type="button"
                  aria-label="Remove {r.document.name}"
                  onclick={() => askRemove(r.document.name)}>Remove</button
                >
              {:else}
                <span class="muted">–</span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <ProblemBox error={removeError} />
{/if}

<style>
  .back {
    margin: 0 0 var(--space-2);
    font-size: var(--text-sm);
  }
  .flush {
    padding: 0;
  }
  .remove {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    min-width: 0;
  }
  .error {
    font-size: var(--text-xs);
    color: var(--color-danger);
  }
  .remove label {
    font-weight: 600;
    font-size: var(--text-sm);
  }
</style>
