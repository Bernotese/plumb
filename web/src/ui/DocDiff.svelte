<script lang="ts">
  // One changed document of a revision diff: a line diff of its YAML text
  // (D-093). Signs, words for screen readers and colour mark each line, so
  // colour is never the only sign.
  import { diffLines, diffStats, withContext } from "../lib/textDiff";
  import type { DocumentChange } from "../lib/types";
  import { toYaml } from "../lib/yaml";
  import Badge from "./Badge.svelte";

  let { change, id }: { change: DocumentChange; id: string } = $props();
  let full = $state(false);

  const lines = $derived(
    diffLines(change.before ? toYaml(change.before) : "", change.after ? toYaml(change.after) : ""),
  );
  const stats = $derived(diffStats(lines));
  const rows = $derived(full ? lines : withContext(lines, 3));
  const hidden = $derived(rows.some((r) => r.op === "skip"));
  const opView = {
    added: { tone: "ok", label: "Added" },
    changed: { tone: "info", label: "Changed" },
    removed: { tone: "danger", label: "Removed" },
  } as const;
  const sign = { same: " ", add: "+", del: "−" } as const;
  const word = { same: "", add: "Added: ", del: "Removed: " } as const;
</script>

<section class="card doc" aria-labelledby="{id}-title">
  <div class="spread">
    <h3 id="{id}-title">
      <Badge tone={opView[change.op].tone} label={opView[change.op].label} />
      <span class="mono">{change.kind} {change.name}</span>
    </h3>
    <p class="stats mono">
      <span class="sr-only">{stats.added} lines added, {stats.removed} lines removed</span>
      <span aria-hidden="true"
        ><span class="add-text">+{stats.added}</span>
        <span class="del-text">−{stats.removed}</span></span
      >
    </p>
  </div>
  <!-- A wide diff scrolls sideways: keyboard users need focus to scroll it (WCAG 2.1.1). -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="scroll"
    role="region"
    aria-label="YAML diff of {change.kind} {change.name}"
    tabindex="0"
  >
    <table class="diff mono">
      <caption class="sr-only">Line diff of {change.kind} {change.name}</caption>
      <thead class="sr-only">
        <tr>
          <th scope="col">Old line</th>
          <th scope="col">New line</th>
          <th scope="col">Text</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as r, i (i)}
          {#if r.op === "skip"}
            <tr class="skip">
              <td colspan="3">
                <button class="link" type="button" onclick={() => (full = true)}>
                  Show {r.count} unchanged lines
                </button>
              </td>
            </tr>
          {:else}
            <tr class={r.op}>
              <td class="no">{r.old ?? ""}</td>
              <td class="no">{r.new ?? ""}</td>
              <td class="text"
                ><span class="sign" aria-hidden="true">{sign[r.op]}</span><span class="sr-only"
                  >{word[r.op]}</span
                >{r.text}</td
              >
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
  </div>
  {#if full && !hidden && lines.some((l) => l.op === "same")}
    <button class="link" type="button" onclick={() => (full = false)}>Hide unchanged lines</button>
  {/if}
</section>

<style>
  .doc {
    padding-bottom: var(--space-3);
  }
  h3 {
    font-size: var(--text-md);
    margin: 0;
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }
  .stats {
    margin: 0;
    font-size: var(--text-sm);
  }
  .add-text {
    color: var(--color-ok);
  }
  .del-text {
    color: var(--color-danger);
  }
  .scroll {
    overflow-x: auto;
    margin-top: var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: 6px;
  }
  .diff {
    border-collapse: collapse;
    width: 100%;
    font-size: var(--text-sm);
    line-height: 1.45;
  }
  .diff td {
    padding: 0 var(--space-2);
    border: 0;
    vertical-align: top;
  }
  .no {
    width: 1%;
    text-align: right;
    color: var(--color-text-muted);
    user-select: none;
    white-space: nowrap;
  }
  .text {
    white-space: pre;
  }
  .sign {
    display: inline-block;
    width: 1.5ch;
    font-weight: 700;
    user-select: none;
  }
  tr.add {
    background: var(--color-ok-bg);
  }
  tr.add .sign {
    color: var(--color-ok);
  }
  tr.del {
    background: var(--color-danger-bg);
  }
  tr.del .sign {
    color: var(--color-danger);
  }
  tr.del .text {
    text-decoration: line-through;
    text-decoration-color: color-mix(in srgb, var(--color-danger) 45%, transparent);
  }
  tr.skip td {
    background: var(--color-surface-2);
    padding: var(--space-1) var(--space-2);
  }
  .link {
    background: none;
    border: 0;
    padding: 0;
    color: var(--color-accent);
    font: inherit;
    font-family: var(--font-sans);
    font-size: var(--text-sm);
    text-decoration: underline;
    cursor: pointer;
    margin-top: var(--space-2);
  }
  tr.skip .link {
    margin: 0;
  }
</style>
