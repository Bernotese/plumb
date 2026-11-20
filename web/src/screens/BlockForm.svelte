<script module lang="ts">
  import resourceKinds from "../lib/resourceKinds.json";
  import type { Field } from "../lib/schemaForm";

  /** One resource of the preview: its type and its key, as the server names it (`file:/etc/motd`). */
  export interface PreviewResource {
    type: string;
    key: string;
    /** The key still holds an action that only the server can fill (a pipe, a function). */
    template: boolean;
    /** Why the server refuses this resource, when the preview knows it. */
    refused?: string;
    /** The Target kinds of the type when it does not fit every kind ("vm"). */
    only?: string;
  }

  /**
   * The key attribute of each resource type. A hand copy of `Type.Key` in
   * internal/resource/resource.go: the tests pin the path-keyed types, so a
   * new type needs a line here (one source, through resourceKinds.json, is a
   * later card).
   */
  const PATH_KEYED = new Set(["file", "directory", "symlink"]);
  export const keyAttr = (type: string) => (PATH_KEYED.has(type) ? "path" : "name");

  const KINDS: Record<string, readonly string[]> = resourceKinds;
  const ALL_KINDS = [...new Set(Object.values(KINDS).flat())];

  /** Go text/template actions; `{{- ` and ` -}}` trim the whitespace next to them. */
  const ACTION = /\{\{(- )?([^]*?)( -)?\}\}/g;
  /** Go string, raw string and rune literals inside an action. */
  const LITERAL = /"(?:[^"\\\n]|\\.)*"|`[^`]*`|'(?:[^'\\\n]|\\.)*'/g;
  const SIMPLE = /^\s*\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*$/;
  const PARAM_REF = /(?:^|[^A-Za-z0-9_$\]])\.([A-Za-z_][A-Za-z0-9_]*)/g;

  export interface Filled {
    text: string;
    /** An action stays that only the server can fill. */
    template: boolean;
    refused?: string;
  }

  /**
   * Fills a template the way the server does where the preview can know it
   * (internal/state/template.go, render.go): a simple `{{ .a.b }}` with a
   * string, number or boolean; whitespace trim marks. The server refuses a
   * parameter that the schema does not name, a value that is missing after
   * the defaults (`missingkey=error`) and any secret parameter in a key
   * (D-088). Pipes, functions and `if` stay as written (`template`).
   */
  export function fillTemplate(
    s: string,
    params: Record<string, unknown>,
    fields: Field[] = [],
  ): Filled {
    let template = false;
    let refused: string | undefined;
    const refuse = (why: string) => (refused ??= why);
    // One action at a time, so that a trim mark trims the text next to it
    // after the action before it was filled (row 38 of the T-260 log):
    // "{{ .a }} {{- .b }}" is "xy", as in Go.
    const fill = (all: string, body: string): string => {
      // A parameter reference inside a string literal is text (row 39).
      for (const m of body.replace(LITERAL, '""').matchAll(PARAM_REF)) {
        const f = fields.find((x) => x.name === m[1]);
        if (!f) refuse(`.${m[1]} is not a parameter`);
        else if (f.kind === "secret") refuse("a key cannot hold a secret");
      }
      const simple = SIMPLE.exec(body);
      if (!simple) {
        template = true;
        return all;
      }
      const path = (simple[1] ?? "").split(".");
      let v: unknown = params;
      let fs: Field[] | undefined = fields;
      for (const k of path) {
        const f: Field | undefined = fs?.find((x) => x.name === k);
        if (f?.kind === "secret") {
          refuse("a key cannot hold a secret");
          return all;
        }
        fs = f?.fields;
        if (typeof v !== "object" || v === null || Array.isArray(v) || !(k in v)) {
          refuse(`no value for .${path.join(".")}`);
          return all;
        }
        v = (v as Record<string, unknown>)[k];
      }
      if (typeof v === "string" || typeof v === "boolean") return String(v);
      if (typeof v === "number" && Number.isFinite(v)) return String(v);
      template = true;
      return all;
    };
    let text = "";
    let last = 0;
    let trimNext = false;
    for (const m of s.matchAll(ACTION)) {
      let between = s.slice(last, m.index);
      if (trimNext) between = between.replace(/^\s+/, "");
      if (m[1]) between = between.replace(/\s+$/, "");
      text += between + fill(m[0], m[2] ?? "");
      trimNext = m[3] !== undefined;
      last = m.index + m[0].length;
    }
    let tail = s.slice(last);
    if (trimNext) tail = tail.replace(/^\s+/, "");
    text += tail;
    return refused ? { text, template, refused } : { text, template };
  }

  /** The parameters as the server renders them: the top-level defaults fill missing values. */
  export function withDefaults(
    params: Record<string, unknown>,
    fields: Field[],
  ): Record<string, unknown> {
    const out = { ...params };
    for (const f of fields)
      if (!(f.name in out) && f.default !== undefined && f.kind !== "secret")
        out[f.name] = f.default;
    return out;
  }

  /** The resources of a Block with their keys filled from the preview parameters, and marks. */
  export function previewResources(
    resources: unknown[],
    params: Record<string, unknown>,
    fields: Field[] = [],
  ): PreviewResource[] {
    const out: PreviewResource[] = [];
    const seen: Record<string, number> = {};
    const data = withDefaults(params, fields);
    for (const r of resources) {
      if (typeof r !== "object" || r === null || Array.isArray(r)) continue;
      const entries = Object.entries(r as Record<string, unknown>);
      if (entries.length !== 1) continue;
      for (const [type, attrs] of entries) {
        const attr = keyAttr(type);
        const raw =
          typeof attrs === "object" && attrs !== null
            ? (attrs as Record<string, unknown>)[attr]
            : undefined;
        const res: PreviewResource = { type, key: "", template: false };
        const kinds = KINDS[type];
        if (!kinds) res.refused = `unknown resource type ${type}`;
        else if (ALL_KINDS.some((k) => !kinds.includes(k))) res.only = kinds.join(", ");
        if (typeof raw !== "string") {
          res.refused ??= `no ${attr}`;
        } else {
          const f = fillTemplate(raw, data, fields);
          res.key = f.text;
          res.template = f.template;
          if (f.refused) res.refused ??= f.refused;
          else if (!f.template && f.text === "") res.refused ??= `empty ${attr}`;
        }
        if (!res.refused && !res.template) {
          const id = `${type}:${res.key}`;
          const before = seen[id];
          if (before !== undefined) res.refused = `the same id as resource ${before}`;
          else seen[id] = out.length + 1;
        }
        out.push(res);
      }
    }
    return out;
  }
</script>

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
  import { fieldsOf, initialValues, toParams, validate, type Values } from "../lib/schemaForm";
  import type { BlockDoc, BlockView } from "../lib/types";
  import { toYaml } from "../lib/yaml";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import SchemaFields from "../ui/SchemaFields.svelte";

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

  // The preview (D-426): the parameter form that a Target gets for this
  // Block (the same schemaForm as the Target form) and the resource list.
  // It is a local approximation: the server renders and validates only when
  // a plan is made (no route renders a Block without storing a Plan).
  const spec = $derived(
    doc && typeof doc.spec === "object" && doc.spec !== null
      ? (doc.spec as Partial<BlockDoc["spec"]>)
      : undefined,
  );
  const preview = $derived.by((): { fields: Field[]; error: boolean } => {
    const p = spec?.params;
    if (p === undefined) return { fields: [], error: false };
    if (typeof p !== "object" || p === null || Array.isArray(p)) return { fields: [], error: true };
    try {
      return { fields: fieldsOf(p), error: false };
    } catch {
      return { fields: [], error: true };
    }
  });
  let previewValues = $state<Values>({});
  let previewFields: Field[] = [];
  let previewShape = "";
  // A new form when the parameter schema changes; values of the old form
  // carry over by name where they still fit.
  $effect.pre(() => {
    const fields = preview.fields;
    const shape = JSON.stringify(fields);
    if (shape === previewShape) return;
    let carry: Record<string, unknown>;
    try {
      carry = toParams(previewFields, $state.snapshot(previewValues) as Values);
    } catch {
      carry = {};
    }
    try {
      previewValues = initialValues(fields, carry);
    } catch {
      previewValues = initialValues(fields);
    }
    previewFields = fields;
    previewShape = shape;
  });
  /** Errors show after the person types in the preview, not on load. */
  let previewTouched = $state(false);
  // The preview has no secret picker (it lists no secrets and makes none),
  // so a secret field shows no error.
  const previewErrors = $derived.by(() => {
    if (!previewTouched) return {};
    const secrets = preview.fields.filter((f) => f.kind === "secret").map((f) => f.name);
    try {
      const all = validate(preview.fields, previewValues);
      return Object.fromEntries(Object.entries(all).filter(([k]) => !secrets.includes(k)));
    } catch {
      return {};
    }
  });
  const previewParams = $derived.by((): Record<string, unknown> => {
    try {
      return toParams(preview.fields, previewValues);
    } catch {
      return {};
    }
  });
  const resourceList = $derived(
    Array.isArray(spec?.resources)
      ? previewResources(spec.resources, previewParams, preview.fields)
      : [],
  );

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
  <div class="split">
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
          <p class="error-title">The text has no change. Change it to make a Plan.</p>
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
        {#if reasonMissing}<span class="error" id="b-reason-error"
            >Say why you make this change.</span
          >{/if}
      </div>
      <ProblemBox
        error={submitError}
        fields={Object.keys(serverErrors).filter((p) => p === "/put/0" || p.startsWith("/put/0/"))}
        shown={uses.handled}
      />
      <div class="row">
        {#if can("createPlan")}<button class="btn primary" type="submit" disabled={busy}
            >Show the Plan</button
          >{/if}
        <a
          class="btn"
          href={editing && name ? href({ name: "block", block: name }) : href({ name: "blocks" })}
          >Cancel</a
        >
      </div>
      <p class="muted">Nothing changes until you approve the Plan.</p>
    </form>
    <aside class="preview" aria-labelledby="b-preview-title">
      <h2 id="b-preview-title">Preview</h2>
      <p class="note" id="b-preview-hint">
        A local approximation while you type. The server renders and checks the Block when you make
        the Plan.
      </p>
      {#if !spec}
        <p class="muted" id="b-preview-none">The preview shows when the text is a Block.</p>
      {:else}
        <h3>Parameters on a Target</h3>
        {#if preview.error}
          <p class="muted" id="b-params-bad">
            The preview cannot read <code>spec.params</code>. It must be an object schema.
          </p>
        {:else if preview.fields.length}
          <form
            class="params"
            novalidate
            onsubmit={(e) => e.preventDefault()}
            oninput={() => (previewTouched = true)}
            onchange={() => (previewTouched = true)}
            id="b-params"
          >
            <SchemaFields
              fields={preview.fields}
              bind:values={previewValues}
              errors={previewErrors}
              prefix="preview"
            />
          </form>
        {:else}
          <p class="muted" id="b-params-none">This Block has no parameters.</p>
        {/if}
        <h3>Resources</h3>
        {#if resourceList.length}
          <ol class="resources mono" id="b-resources">
            {#each resourceList as r, i (i)}
              <li>
                <span>{r.type}:{r.key || "?"}</span>
                {#if r.template}<span class="muted tpl">(template)</span>{/if}
                {#if r.only}<span class="muted tpl">({r.only} only)</span>{/if}
                {#if r.refused}<span class="refused tpl">(refused: {r.refused})</span>{/if}
              </li>
            {/each}
          </ol>
          <p class="note">
            Keys from the parameters above. "(template)": only the server can fill it. "(refused:
            …)": the server refuses this Block on a Target. A secret parameter never shows its value
            here.
          </p>
        {:else}
          <p class="muted" id="b-resources-none">No resources yet.</p>
        {/if}
      {/if}
    </aside>
  </div>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .split {
    display: grid;
    gap: var(--space-6);
    grid-template-columns: minmax(0, 1fr);
  }
  @media (min-width: 1100px) {
    .split {
      grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
      align-items: start;
    }
  }
  .preview {
    border: 1px solid var(--color-border);
    border-radius: var(--radius, 6px);
    padding: var(--space-4);
    background: var(--color-surface);
  }
  .preview .note {
    font-size: var(--text-sm);
    color: var(--color-text-muted);
  }
  .preview h2 {
    margin-top: 0;
  }
  .preview h3 {
    margin: var(--space-4) 0 var(--space-2);
    font-size: var(--text-base, 1rem);
  }
  .tpl {
    margin-left: 0.5ch;
  }
  .refused {
    color: var(--color-danger);
  }
  .resources {
    margin: 0;
    padding-left: var(--space-6);
    overflow-wrap: anywhere;
  }
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
