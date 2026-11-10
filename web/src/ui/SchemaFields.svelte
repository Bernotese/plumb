<script lang="ts">
  import type { Field, Values } from "../lib/schemaForm";
  import StringList from "./StringList.svelte";

  let {
    fields,
    values = $bindable(),
    errors = {},
    prefix,
    readonly = [],
    readonlyHint = "Cannot change after create.",
  }: {
    fields: Field[];
    values: Values;
    errors?: Record<string, string>;
    prefix: string;
    readonly?: readonly string[];
    readonlyHint?: string;
  } = $props();

  const fid = (f: Field) => `${prefix}-${f.name}`;
  const ro = (f: Field) => readonly.includes(f.name);
  // The read-only hint is left out when the description already says it.
  function hintText(f: Field): string {
    const d = f.description ?? "";
    if (!ro(f) || /create-only/i.test(d)) return d;
    return `${readonlyHint} ${d}`.trim();
  }
  function describe(f: Field): string | undefined {
    const ids = [];
    if (f.description || ro(f)) ids.push(`${fid(f)}-hint`);
    if (errors[f.name]) ids.push(`${fid(f)}-error`);
    return ids.length ? ids.join(" ") : undefined;
  }
</script>

<div class="grid">
  {#each fields as f (f.name)}
    <div class="field" class:wide={f.kind === "textarea" || f.kind === "list"}>
      {#if f.kind === "boolean"}
        <label class="check">
          <input
            type="checkbox"
            id={fid(f)}
            bind:checked={values[f.name] as boolean}
            disabled={ro(f)}
            aria-describedby={describe(f)}
          />
          {f.label}
        </label>
      {:else if f.kind === "list"}
        <span class="label" id="{fid(f)}-label">
          {f.label}{#if f.required}<span aria-hidden="true"> *</span><span class="sr-only">
              (required)</span
            >{/if}
        </span>
        <StringList
          id={fid(f)}
          label={f.label}
          bind:items={values[f.name] as string[]}
          readonly={ro(f)}
          describedby={describe(f)}
          invalid={!!errors[f.name]}
        />
      {:else}
        <label for={fid(f)}>
          {f.label}{#if f.required}<span aria-hidden="true"> *</span>{/if}
        </label>
        {#if f.kind === "enum"}
          <select
            id={fid(f)}
            bind:value={values[f.name]}
            disabled={ro(f)}
            required={f.required}
            aria-invalid={!!errors[f.name]}
            aria-describedby={describe(f)}
          >
            {#if !f.required}<option value="">(none)</option>{/if}
            {#each f.options ?? [] as o (o)}<option value={o}>{o}</option>{/each}
          </select>
        {:else if f.kind === "textarea"}
          <textarea
            id={fid(f)}
            bind:value={values[f.name]}
            readonly={ro(f)}
            required={f.required}
            aria-invalid={!!errors[f.name]}
            aria-describedby={describe(f)}></textarea>
        {:else if f.kind === "unsupported"}
          <p class="muted" id={fid(f)}>This parameter type needs raw mode (later).</p>
        {:else}
          <input
            type="text"
            inputmode={f.kind === "integer"
              ? "numeric"
              : f.kind === "number"
                ? "decimal"
                : undefined}
            id={fid(f)}
            bind:value={values[f.name]}
            readonly={ro(f)}
            required={f.required}
            aria-invalid={!!errors[f.name]}
            aria-describedby={describe(f)}
          />
        {/if}
      {/if}
      {#if f.description || ro(f)}
        <span class="hint" id="{fid(f)}-hint">{hintText(f)}</span>
      {/if}
      {#if errors[f.name]}
        <span class="error" id="{fid(f)}-error">{errors[f.name]}</span>
      {/if}
    </div>
  {/each}
</div>

<style>
  .wide {
    grid-column: 1 / -1;
  }
  .check {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-weight: 600;
    font-size: var(--text-sm);
  }
</style>
