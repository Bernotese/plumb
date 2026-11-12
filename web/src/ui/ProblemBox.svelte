<script lang="ts">
  import { missingText, outdatedOf } from "../lib/agent";
  import { ApiError, NetworkError } from "../lib/api";
  import { href } from "../lib/router";

  /** `fields`: pointers that the form shows at its fields; they are not listed here. */
  let { error, fields = [] }: { error: unknown; fields?: string[] } = $props();
  const p = $derived(error instanceof ApiError ? error.problem : undefined);
  const rest = $derived(p?.errors?.filter((e) => !fields.includes(e.pointer)) ?? []);
  const atFields = $derived((p?.errors?.length ?? 0) > rest.length);
  // Only the HTTP client marks a failed fetch; any other error is a bug.
  const network = $derived(error instanceof NetworkError);
  // The gate refusal (D-259) links to the agent of each Target (T-097).
  const outdated = $derived(outdatedOf(error));
  const text = $derived(error instanceof Error ? error.message : String(error));
</script>

{#if error}
  <div class="banner danger" role="alert">
    {#if p}
      <strong>{p.title}.</strong>
      {#if p.detail}{p.detail}{/if}
      {#if outdated}
        {#if outdated.missing.length}
          <p class="list">
            The agent lacks: {outdated.missing.map(missingText).join(", ")}.
          </p>
        {/if}
        <ul class="agents">
          {#each outdated.targets as t (t)}
            <li>
              <a href={href({ name: "target-agent", target: t })}>Update the agent of {t}</a>
            </li>
          {/each}
        </ul>
      {:else if p.targets?.length}
        <p class="list">Targets: <span class="mono">{p.targets.join(", ")}</span></p>
      {/if}
      {#if p.plans?.length}
        <p class="list">Pending plans: {p.plans.map((id) => `Plan ${id}`).join(", ")}</p>
      {/if}
      {#if p.secrets?.length}
        <p class="list">Secrets: <span class="mono">{p.secrets.join(", ")}</span></p>
      {/if}
      {#if atFields}Check the marked fields.{/if}
      {#if rest.length}
        <ul>
          {#each rest as e, i (i)}
            <li><span class="mono">{e.pointer}</span>: {e.message}</li>
          {/each}
        </ul>
      {/if}
    {:else if network}
      <strong>The Plumb API is not reachable.</strong> Check the connection and try again.
    {:else}
      <strong>Unexpected error.</strong> {text}
    {/if}
  </div>
{/if}

<style>
  .list {
    margin: var(--space-1) 0 0;
  }
  .agents {
    margin: var(--space-1) 0 0;
  }
  .agents a {
    color: inherit;
    font-weight: 600;
  }
</style>
