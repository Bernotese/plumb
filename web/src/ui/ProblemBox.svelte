<script lang="ts">
  import { ApiError, NetworkError } from "../lib/api";

  let { error }: { error: unknown } = $props();
  const p = $derived(error instanceof ApiError ? error.problem : undefined);
  // Only the HTTP client marks a failed fetch; any other error is a bug.
  const network = $derived(error instanceof NetworkError);
  const text = $derived(error instanceof Error ? error.message : String(error));
</script>

{#if error}
  <div class="banner danger" role="alert">
    {#if p}
      <strong>{p.title}.</strong>
      {#if p.detail}{p.detail}{/if}
      {#if p.targets?.length}
        <p class="list">Targets: <span class="mono">{p.targets.join(", ")}</span></p>
      {/if}
      {#if p.plans?.length}
        <p class="list">Pending plans: {p.plans.map((id) => `Plan ${id}`).join(", ")}</p>
      {/if}
      {#if p.secrets?.length}
        <p class="list">Secrets: <span class="mono">{p.secrets.join(", ")}</span></p>
      {/if}
      {#if p.errors?.length}
        <ul>
          {#each p.errors as e, i (i)}
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
</style>
