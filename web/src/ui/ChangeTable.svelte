<script lang="ts">
  import type { Change } from "../lib/types";
  import { changeNotes, changeView } from "../lib/view";
  import RiskBadge from "./RiskBadge.svelte";

  let {
    changes,
    caption,
    fromLabel = "Now",
    toLabel = "Desired",
    resource,
  }: {
    changes: Change[];
    caption: string;
    fromLabel?: string;
    toLabel?: string;
    /** The resource id: guest:config changes get the T-056 tags and texts. */
    resource?: string;
  } = $props();
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->

<div class="table-wrap" tabindex="0" role="group" aria-label="{caption}, scrolls sideways">
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
        {@const v = changeView(c, resource)}
        {@const n = changeNotes(c, resource)}
        <tr>
          <th scope="row" class:mono={v.attr === c.attr}
            >{v.attr}{#each n.tags as tag (tag)}<span class="tag">{tag}</span
              >{/each}{#if c.ignored}<span class="ignored"
                >A rule ignores this drift; this apply overwrites it.</span
              >{/if}</th
          >
          {#if v.hidden}
            <td colspan="2"><em>changed (hidden)</em></td>
          {:else if v.restart}
            <td colspan="2"
              ><em>{n.notes.length ? "Restart of the VM" : "Restart (no value change)"}</em></td
            >
          {:else}
            <td><pre class="mono value">{v.from}</pre></td>
            <td><pre class="mono value">{v.to}</pre></td>
          {/if}
          <td><RiskBadge risk={c.risk} /></td>
        </tr>
        {#if n.notes.length}
          <tr class="note-row">
            <td colspan="4">
              {#each n.notes as note, j (j)}<p class="note">{note}</p>{/each}
            </td>
          </tr>
        {/if}
      {:else}
        <tr><td colspan="4" class="muted">No attribute changes.</td></tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .tag {
    display: inline-block;
    margin-left: var(--space-2);
    padding: 0 var(--space-1);
    border: 1px solid var(--color-warn);
    border-radius: var(--radius, 4px);
    font-family: var(--font-sans);
    font-size: var(--text-xs);
    font-weight: 600;
    color: var(--color-warn);
    white-space: nowrap;
  }
  .note-row td {
    border-top: none;
    padding-top: 0;
  }
  .note {
    margin: 0 0 var(--space-1);
    font-size: var(--text-sm);
    color: var(--color-text-muted);
  }
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
