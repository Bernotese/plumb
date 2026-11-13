<script lang="ts">
  // The agent of a Target (T-097): version, update state, what it lacks, a
  // revert, and "Propose agent update", which opens the Plan (D-257: no
  // silent action; the update runs only after an approval).
  import { agentState, missingText, revertReason } from "../lib/agent";
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { go } from "../lib/router";
  import type { TargetView } from "../lib/types";
  import { formatTime } from "../lib/view";
  import Badge from "./Badge.svelte";
  import ProblemBox from "./ProblemBox.svelte";

  let {
    api,
    target,
    headingLevel = 2,
  }: { api: PlumbApi; target: TargetView; headingLevel?: 1 | 2 } = $props();

  const name = $derived(target.document.name);
  const st = $derived(target.status);
  const agent = $derived(agentState(st));
  let reason = $state("");
  let busy = $state(false);
  let error = $state<unknown>(null);
  let nothing = $state(false);

  async function propose(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = null;
    nothing = false;
    try {
      const plan = await api.updateTargetAgent(name, reason.trim() || undefined);
      go({ name: "plan", id: plan.id });
    } catch (err) {
      if (err instanceof ApiError && err.problem.type.endsWith(":nothing-to-do")) nothing = true;
      else error = err;
    } finally {
      busy = false;
    }
  }
</script>

<section
  class="card agent"
  class:alert={st.agent_update === "required"}
  aria-labelledby={headingLevel === 1 ? "page-title" : "agent-title"}
>
  <div class="spread">
    {#if headingLevel === 1}
      <h1 id="page-title" tabindex="-1">Agent of <span class="mono">{name}</span></h1>
    {:else}
      <h2 id="agent-title">Agent</h2>
    {/if}
    {#if agent?.label}<Badge tone={agent.tone} label={agent.label} />{/if}
  </div>
  <dl class="facts">
    <dt>Version</dt>
    <dd class="mono">{st.agent_version ?? "–"}</dd>
    <dt>Connection</dt>
    <dd>
      {st.agent === "online" ? "online" : st.agent === "offline" ? "offline" : "no agent yet"}
      {#if st.last_seen}<span class="muted"> · last seen {formatTime(st.last_seen)}</span>{/if}
    </dd>
  </dl>
  {#if agent}
    <p id="agent-state">{agent.text}</p>
  {:else}
    <p class="muted">This controller does not report agent updates.</p>
  {/if}
  {#if st.agent_missing?.length}
    <p class="missing-title">The agent lacks:</p>
    <ul class="missing">
      {#each st.agent_missing as m (m)}<li>
          <span class="mono">{m}</span> ({missingText(m)})
        </li>{/each}
    </ul>
  {/if}
  {#if st.agent_reverted}
    {@const r = st.agent_reverted}
    <p class="warn" id="agent-reverted">
      The last update to <span class="mono">{r.to}</span> failed in the guest ({revertReason(
        r.reason,
      )}). The agent went back to <span class="mono">{r.from}</span> at {formatTime(r.at)}.
    </p>
  {/if}
  {#if agent?.canUpdate}
    <form onsubmit={propose}>
      <div class="field">
        <label for="agent-reason">Reason (optional)</label>
        <textarea id="agent-reason" bind:value={reason} maxlength="2000"></textarea>
      </div>
      <ProblemBox {error} />
      {#if nothing}
        <p class="banner ok" role="status">The agent is current already. Reload the page.</p>
      {/if}
      <button class="btn primary" type="submit" disabled={busy}>Propose agent update</button>
      <p class="muted">
        This opens a plan. The agent restarts with the new binary only after you approve it; if the
        new binary fails, the guest goes back to the old one.
      </p>
    </form>
  {/if}
</section>

<style>
  .agent {
    margin-bottom: var(--space-4);
  }
  .agent h2,
  .agent h1 {
    margin-top: 0;
  }
  .alert {
    border-left: 4px solid var(--color-danger);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-1) var(--space-3);
    margin: 0 0 var(--space-3);
  }
  .facts dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .missing-title {
    margin-bottom: 0;
  }
  .missing {
    margin-top: var(--space-1);
    padding-left: var(--space-6);
  }
  .warn {
    color: var(--color-warn);
  }
</style>
