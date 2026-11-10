<script lang="ts">
  import { ApiError } from "../lib/api";

  let { error }: { error: unknown } = $props();
  const p = $derived(error instanceof ApiError ? error.problem : undefined);
  // fetch rejects with a TypeError when the server cannot be reached.
  const network = $derived(!p && error instanceof TypeError);
  const text = $derived(error instanceof Error ? error.message : String(error));
</script>

{#if error}
  <div class="banner danger" role="alert">
    {#if p}
      <strong>{p.title}.</strong>
      {#if p.detail}{p.detail}{/if}
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
