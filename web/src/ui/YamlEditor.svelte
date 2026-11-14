<script lang="ts">
  // The YAML mode of a document form (D-363): one text field with line
  // numbers, and a list of problems that moves the caret to the line. Local
  // problems come from the strict parser (yaml.ts), server problems from the
  // 422 pointers of the proposal.
  import type { LineError } from "../lib/yaml";

  let {
    id,
    label,
    text = $bindable(),
    errors = [],
    serverErrors = [],
    hint,
  }: {
    id: string;
    label: string;
    text: string;
    errors?: LineError[];
    serverErrors?: LineError[];
    hint: string;
  } = $props();

  let editor = $state<HTMLTextAreaElement | undefined>();
  let gutter = $state<HTMLDivElement | undefined>();
  const count = $derived(errors.length + serverErrors.length);
  const lineCount = $derived(text.split("\n").length);
  const badLines = $derived(new Set([...errors, ...serverErrors].map((e) => e.line)));
  const lineText = (n: number) => (n > 0 ? `Line ${n}` : "The text");

  /** Moves the caret to a line and scrolls it into view. */
  function goToLine(line: number) {
    if (!editor) return;
    const lines = text.split("\n");
    const start = lines.slice(0, Math.max(0, line - 1)).reduce((n, l) => n + l.length + 1, 0);
    const end = start + (lines[line - 1]?.length ?? 0);
    editor.focus();
    editor.setSelectionRange(start, end);
    const lh = parseFloat(getComputedStyle(editor).lineHeight) || 20;
    editor.scrollTop = Math.max(0, (line - 3) * lh);
  }
  function syncScroll() {
    if (gutter && editor) gutter.scrollTop = editor.scrollTop;
  }
</script>

<div class="field wide">
  <label for={id}>{label}</label>
  <div class="editor" class:invalid={count > 0}>
    <div class="gutter mono" aria-hidden="true" bind:this={gutter}>
      {#each Array.from({ length: lineCount }, (_x, i) => i + 1) as n (n)}
        <span class:bad={badLines.has(n)}>{badLines.has(n) ? "!" : ""}{n}</span>
      {/each}
    </div>
    <textarea
      {id}
      class="mono"
      bind:this={editor}
      bind:value={text}
      onscroll={syncScroll}
      spellcheck="false"
      autocapitalize="off"
      autocomplete="off"
      wrap="off"
      rows="24"
      aria-invalid={count > 0}
      aria-describedby="{id}-hint{count ? ` ${id}-errors` : ''}"></textarea>
  </div>
  <span class="hint" id="{id}-hint">{hint}</span>
</div>

<div id="{id}-errors" tabindex="-1" class="errors" aria-live="polite">
  {#if count > 0}
    <p class="error-title">
      {count} problem{count === 1 ? "" : "s"}. Select one to go to its line.
    </p>
    <ul>
      {#each errors as e, i (i)}
        <li>
          <button type="button" class="link" onclick={() => goToLine(e.line)}
            >{lineText(e.line)}</button
          >: {e.message}
        </li>
      {/each}
      {#each serverErrors as e, i (i)}
        <li>
          <button type="button" class="link" onclick={() => goToLine(e.line)}
            >{lineText(e.line)}</button
          >: {e.message} <span class="muted">(server)</span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .editor {
    display: flex;
    border: 1px solid var(--color-border);
    border-radius: var(--radius, 6px);
    background: var(--color-surface);
    overflow: hidden;
  }
  .editor.invalid {
    border-color: var(--color-danger);
  }
  .editor:focus-within {
    outline: 2px solid var(--color-focus);
    outline-offset: 1px;
  }
  .gutter,
  .editor textarea {
    font-size: var(--text-sm);
    line-height: 1.5;
    padding: var(--space-2) 0;
  }
  .gutter {
    display: flex;
    flex-direction: column;
    flex: none;
    min-width: 3.5em;
    text-align: right;
    padding-right: var(--space-2);
    padding-left: var(--space-2);
    color: var(--color-text-muted);
    background: var(--color-muted-bg);
    overflow: hidden;
    user-select: none;
  }
  .gutter span.bad {
    color: var(--color-danger);
    font-weight: 700;
  }
  .editor textarea {
    flex: 1;
    min-width: 0;
    border: 0;
    border-radius: 0;
    resize: vertical;
    min-height: 24em;
    padding-left: var(--space-3);
    padding-right: var(--space-3);
    white-space: pre;
    overflow: auto;
    tab-size: 2;
    background: transparent;
    color: var(--color-text);
  }
  .editor textarea:focus {
    outline: none;
    box-shadow: none;
  }
  .errors {
    margin: var(--space-3) 0 var(--space-4);
  }
  .errors:focus {
    outline: 2px solid var(--color-focus);
  }
  .errors ul {
    margin: var(--space-1) 0 0;
    padding-left: var(--space-6);
    color: var(--color-danger);
  }
  .error-title {
    margin: 0;
    color: var(--color-danger);
    font-weight: 600;
  }
  .link {
    background: none;
    border: 0;
    padding: 0;
    font: inherit;
    font-weight: 600;
    color: inherit;
    text-decoration: underline;
    cursor: pointer;
  }
  .link:focus-visible {
    outline: 2px solid var(--color-accent);
  }
</style>
