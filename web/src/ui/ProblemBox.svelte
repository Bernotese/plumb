<script lang="ts">
  import { ApiError } from "../lib/api";

  let { error }: { error: unknown } = $props();
  const p = $derived(error instanceof ApiError ? error.problem : undefined);
</script>

{#if error}
  <div class="banner danger" role="alert">
    {#if p}
      <strong>{p.title}.</strong>
      {#if p.detail}{p.detail}{/if}
      {#if p.errors?.length}
        <ul>
          {#each p.errors as e (e.pointer)}
            <li><span class="mono">{e.pointer}</span>: {e.message}</li>
          {/each}
        </ul>
      {/if}
    {:else}
      <strong>Something went wrong.</strong> The Plumb API is not reachable. Try again later.
    {/if}
  </div>
{/if}
