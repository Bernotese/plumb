<script lang="ts">
  import type { Change } from "../lib/types";
  import { changeView } from "../lib/view";
  import RiskBadge from "./RiskBadge.svelte";

  let {
    changes,
    caption,
    fromLabel = "Now",
    toLabel = "Desired",
  }: { changes: Change[]; caption: string; fromLabel?: string; toLabel?: string } = $props();
</script>

<div class="table-wrap">
  <table>
    <caption class="sr-only">{caption}</caption>
    <thead>
      <tr>
        <th scope="col">Attribute</th>
        <th scope="col">{fromLabel}</th>
        <th scope="col">{toLabel}</th>
        <th scope="col">Risk</th>
      </tr>
    </thead>
    <tbody>
      {#each changes as c, i (i)}
        {@const v = changeView(c)}
        <tr>
          <th scope="row" class="mono"
            >{v.attr}{#if c.ignored}<span class="ignored"
                >A rule ignores this drift; this apply overwrites it.</span
              >{/if}</th
          >
          {#if v.hidden}
            <td colspan="2"><em>changed (hidden)</em></td>
          {:else if v.restart}
            <td colspan="2"><em>Restart (no value change)</em></td>
          {:else}
            <td><pre class="mono value">{v.from}</pre></td>
            <td><pre class="mono value">{v.to}</pre></td>
          {/if}
          <td><RiskBadge risk={c.risk} /></td>
        </tr>
      {:else}
        <tr><td colspan="4" class="muted">No attribute changes.</td></tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .ignored {
    display: block;
    font-family: var(--font-sans);
    font-size: var(--text-xs);
    font-weight: 400;
    color: var(--color-warn);
    white-space: normal;
  }
  .value {
    margin: 0;
    white-space: pre-wrap;
    max-height: 12rem;
    overflow: auto;
  }
</style>
