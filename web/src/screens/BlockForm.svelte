<script lang="ts">
  // The Block editor (D-261): the Block is YAML text. The browser checks it
  // against block.json while the user types; the server checks it again and
  // its 422 pointers show at their lines too. Save is a normal proposal.
  import { ApiError } from "../lib/api";
  import { docPointers, NEW_BLOCK, useErrors, usersOf, type Users } from "../lib/blocks";
  import { parseBlock, type LineError } from "../lib/blockYaml";
  import type { PlumbApi } from "../lib/client";
  import { can } from "../lib/session.svelte";
  import { pointerErrors } from "../lib/formErrors";
  import { go, href } from "../lib/router";
  import type { BlockDoc, BlockView } from "../lib/types";
  import { toYaml } from "../lib/yaml";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, name }: { api: PlumbApi; name?: string } = $props();
  const editing = $derived(name !== undefined);

  let all = $state<BlockView[]>([]);
  let users = $state<Users | null>(null);
  let loaded = $state(false);
  let loadError = $state<unknown>(null);
  let original = $state("");
  let text = $state("");
  let reason = $state("");
  let reasonMissing = $state(false);
  /** Errors show after the first blur or save, then live while typing. */
  let checked = $state(false);
  let submitError = $state<unknown>(null);
  let busy = $state(false);
  /** A save without a change: there is nothing to plan. */
  let unchangedTried = $state(false);
  let editor = $state<HTMLTextAreaElement | null>(null);
  let gutter = $state<HTMLElement | null>(null);

  $effect(() => {
    Promise.all([api.listBlocks(), api.listTargets(), api.listImages()]).then(
      ([b, t, i]) => {
        all = b;
        const current = name ? b.find((x) => x.document.name === name) : undefined;
        if (name && !current) {
          loadError = new Error(`Block "${name}" does not exist.`);
          return;
        }
        text = original = current ? toYaml(current.document) : NEW_BLOCK;
        if (name) users = usersOf(name, t, i);
        loaded = true;
      },
      (e: unknown) => (loadError = e),
    );
  });

  const parsed = $derived(parseBlock(text));
  const doc = $derived(
    parsed.value !== null && typeof parsed.value === "object"
      ? (parsed.value as Partial<BlockDoc>)
      : undefined,
  );
  /** Checks of this screen that block.json cannot make. */
  const screenErrors = $derived.by((): LineError[] => {
    if (!doc || typeof doc.name !== "string") return [];
    const line = parsed.lineOf("/name");
    if (editing && doc.name !== name)
      return [
        {
          line,
          pointer: "/name",
          message: `The name must stay "${name}". To copy this Block, make a new one.`,
        },
      ];
    if (!editing && all.some((b) => b.document.name === doc.name))
      return [{ line, pointer: "/name", message: `A Block "${doc.name}" exists already.` }];
    return [];
  });
  const serverErrors = $derived(pointerErrors(submitError));
  // Errors of the Targets and Images that use the Block name its resources (D-265).
  const uses = $derived(
    useErrors(
      submitError instanceof ApiError ? (submitError.problem.errors ?? []) : [],
      name ?? (typeof doc?.name === "string" ? doc.name : ""),
      parsed.value,
    ),
  );
  const serverAtLines = $derived([
    ...Object.entries(docPointers(serverErrors)).map(([p, message]): LineError => ({
      line: parsed.lineOf(p),
      pointer: p,
      message,
    })),
    ...uses.found.map((u): LineError => ({
      line: parsed.lineOf(u.pointer),
      pointer: u.pointer,
      message: u.docs.length ? `${u.message} (in ${u.docs.join(", ")})` : u.message,
    })),
  ]);
  const localErrors = $derived([...parsed.errors, ...screenErrors].sort((a, b) => a.line - b.line));
  const shown = $derived(checked ? localErrors : []);
  const lineCount = $derived(text.split("\n").length);
  const badLines = $derived(new Set([...shown, ...serverAtLines].map((e) => e.line)));
  const changed = $derived(text !== original);

  /** Moves the caret to the start of a line and scrolls it into view. */
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

  async function submit(ev: SubmitEvent) {
    ev.preventDefault();
    checked = true;
    submitError = null;
    reasonMissing = !reason.trim();
    unchangedTried = editing && !changed;
    if (localErrors.length || unchangedTried) {
      queueMicrotask(() => document.getElementById("b-errors")?.focus());
      return;
    }
    if (reasonMissing) {
      document.getElementById("b-reason")?.focus();
      return;
    }
    busy = true;
    try {
      const plan = await api.createPlan({ reason: reason.trim(), put: [doc as BlockDoc] });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      submitError = err;
      queueMicrotask(() => document.getElementById("b-errors")?.focus());
    } finally {
      busy = false;
    }
  }

  const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;
  const lineText = (l: number) => (l > 0 ? `Line ${l}` : "Text");
</script>

{#if editing && name}
  <p class="back"><a href={href({ name: "block", block: name })}>← Block {name}</a></p>
{:else}
  <p class="back"><a href={href({ name: "blocks" })}>← Blocks</a></p>
{/if}
<h1 id="page-title" tabindex="-1">{editing ? `Edit block ${name}` : "New block"}</h1>
<ProblemBox error={loadError} />
{#if loaded}
  {#if users}
    <div class="banner info">
      {#if users.targets.length}
        Saving plans {plural(users.targets.length, "Target")} that use this Block:
        <span class="mono">{users.targets.join(", ")}</span>.
      {:else}
        No Target uses this Block.
      {/if}
      {#if users.images.length}
        Images with this Block in their recipe build a new version:
        <span class="mono">{users.images.join(", ")}</span>.
      {/if}
    </div>
  {/if}
  <form onsubmit={submit} novalidate>
    <div class="field">
      <label for="b-text">Block (YAML)<span aria-hidden="true"> *</span></label>
      <div class="editor" class:invalid={shown.length + serverAtLines.length > 0}>
        <div class="gutter mono" aria-hidden="true" bind:this={gutter}>
          {#each Array.from({ length: lineCount }, (_x, i) => i + 1) as n (n)}
            <span class:bad={badLines.has(n)}>{badLines.has(n) ? "!" : ""}{n}</span>
          {/each}
        </div>
        <textarea
          id="b-text"
          class="mono"
          bind:this={editor}
          bind:value={text}
          onscroll={syncScroll}
          onblur={() => (checked = true)}
          spellcheck="false"
          autocapitalize="off"
          autocomplete="off"
          wrap="off"
          rows="24"
          aria-invalid={shown.length + serverAtLines.length > 0}
          aria-describedby="b-text-hint{shown.length + serverAtLines.length ? ' b-errors' : ''}"
        ></textarea>
      </div>
      <span class="hint" id="b-text-hint"
        >YAML 1.2. Indent with spaces; Tab moves to the next field. Quote a file mode (<code
          >"0644"</code
        >). Values are Go templates over the parameters (<code>{"{{ .name }}"}</code>). The server
        also checks the resource attributes and the parameter schema.</span
      >
    </div>

    <div id="b-errors" tabindex="-1" class="errors" aria-live="polite">
      {#if shown.length + serverAtLines.length > 0}
        <p class="error-title">
          {plural(shown.length + serverAtLines.length, "problem")}. Select one to go to its line.
        </p>
        <ul>
          {#each shown as e, i (i)}
            <li>
              <button type="button" class="link" onclick={() => goToLine(e.line)}
                >{lineText(e.line)}</button
              >: {e.message}
            </li>
          {/each}
          {#each serverAtLines as e, i (i)}
            <li>
              <button type="button" class="link" onclick={() => goToLine(e.line)}
                >{lineText(e.line)}</button
              >: {e.message} <span class="muted">(server)</span>
            </li>
          {/each}
        </ul>
      {:else if unchangedTried && !changed}
        <p class="error-title">The text has no change. Change it to make a plan.</p>
      {:else if checked && loaded}
        <p class="ok">The text matches the Block schema.</p>
      {/if}
    </div>

    <div class="field">
      <label for="b-reason">Reason<span aria-hidden="true"> *</span></label>
      <textarea
        id="b-reason"
        bind:value={reason}
        required
        aria-invalid={reasonMissing}
        aria-describedby="b-reason-hint{reasonMissing ? ' b-reason-error' : ''}"></textarea>
      <span class="hint" id="b-reason-hint">Stored with the revision (provenance).</span>
      {#if reasonMissing}<span class="error" id="b-reason-error">Say why you make this change.</span
        >{/if}
    </div>
    <ProblemBox
      error={submitError}
      fields={Object.keys(serverErrors).filter((p) => p === "/put/0" || p.startsWith("/put/0/"))}
      shown={uses.handled}
    />
    <div class="row">
      {#if can("createPlan")}<button class="btn primary" type="submit" disabled={busy}
          >Show the plan</button
        >{/if}
      <a
        class="btn"
        href={editing && name ? href({ name: "block", block: name }) : href({ name: "blocks" })}
        >Cancel</a
      >
    </div>
    <p class="muted">Nothing changes until you approve the plan.</p>
  </form>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

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
  .ok {
    margin: 0;
    color: var(--color-ok);
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
