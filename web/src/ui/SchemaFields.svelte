<script lang="ts">
  // The fields of a JSON schema (schemaForm.ts). Nested objects render this
  // component again; arrays of objects and key/value maps have their own
  // parts (D-363). `errors` uses field paths: `site/name`, `upstreams/0/host`.
  import type { PlumbApi } from "../lib/client";
  import { initialValues, type Field, type Item, type Row, type Values } from "../lib/schemaForm";
  import KeyValueRows from "./KeyValueRows.svelte";
  import ObjectList from "./ObjectList.svelte";
  import SchemaFields from "./SchemaFields.svelte";
  import SecretPicker from "./SecretPicker.svelte";
  import StringList from "./StringList.svelte";

  let {
    fields,
    values = $bindable(),
    errors = {},
    prefix,
    readonly = [],
    readonlyHint = "Cannot change after create.",
    api,
    notes = {},
  }: {
    fields: Field[];
    values: Values;
    errors?: Record<string, string>;
    prefix: string;
    readonly?: readonly string[];
    readonlyHint?: string;
    /** Needed for secret parameters (secret picker). */
    api?: PlumbApi;
    /** Extra warnings per field name, shown under the field (for example D-192). */
    notes?: Record<string, string>;
  } = $props();

  const fid = (f: Field) => `${prefix}-${f.name}`;
  const NESTED = ["object", "objects", "map"];
  /** The errors under a field, with paths relative to it. */
  const sub = (f: Field) =>
    Object.fromEntries(
      Object.entries(errors)
        .filter(([k]) => k.startsWith(`${f.name}/`))
        .map(([k, v]) => [k.slice(f.name.length + 1), v]),
    );
  const ro = (f: Field) => readonly.includes(f.name);
  // The read-only hint is left out when the description already says it.
  function hintText(f: Field): string {
    const d =
      f.kind === "secret"
        ? `${f.description ?? ""} Plumb saves only the secret name, never the value.`.trim()
        : f.kind === "yaml"
          ? `${f.description ?? ""} YAML; Plumb checks it when you save. A secret is written as {secret: name}.`.trim()
          : (f.description ?? "");
    if (!ro(f) || /create-only/i.test(d)) return d;
    return `${readonlyHint} ${d}`.trim();
  }
  function describe(f: Field): string | undefined {
    const ids = [];
    if (f.description || ro(f) || f.kind === "secret" || f.kind === "yaml")
      ids.push(`${fid(f)}-hint`);
    if (notes[f.name]) ids.push(`${fid(f)}-note`);
    if (errors[f.name]) ids.push(`${fid(f)}-error`);
    return ids.length ? ids.join(" ") : undefined;
  }
</script>

<div class="grid">
  {#each fields as f (f.name)}
    <div
      class="field"
      class:wide={f.kind === "textarea" ||
        f.kind === "list" ||
        f.kind === "secret" ||
        f.kind === "yaml" ||
        NESTED.includes(f.kind)}
    >
      {#if NESTED.includes(f.kind)}
        <fieldset class="nested" aria-describedby={describe(f)} disabled={ro(f)}>
          <legend id="{fid(f)}-legend" tabindex="-1">
            {f.label}{#if f.required}<span aria-hidden="true"> *</span><span class="sr-only">
                (required)</span
              >{/if}
          </legend>
          {#if f.kind === "object"}
            {#if values[f.name] === null}
              <button
                class="btn"
                type="button"
                id="{fid(f)}-set"
                onclick={() => (values[f.name] = initialValues(f.fields ?? [], {}))}
                >Add {f.label.toLowerCase()}</button
              >
            {:else}
              <SchemaFields
                fields={f.fields ?? []}
                bind:values={values[f.name] as Values}
                errors={sub(f)}
                prefix={fid(f)}
                {api}
              />
              {#if !f.required && !ro(f)}
                <button
                  class="btn danger"
                  type="button"
                  id="{fid(f)}-unset"
                  onclick={() => (values[f.name] = null)}>Remove {f.label.toLowerCase()}</button
                >
              {/if}
            {/if}
          {:else if f.kind === "objects"}
            <ObjectList
              field={f}
              bind:items={values[f.name] as Item[]}
              prefix={fid(f)}
              errors={sub(f)}
              {api}
              readonly={ro(f)}
            />
          {:else}
            <KeyValueRows
              field={f}
              bind:rows={values[f.name] as Row[]}
              prefix={fid(f)}
              errors={sub(f)}
              readonly={ro(f)}
            />
          {/if}
        </fieldset>
      {:else if f.kind === "boolean"}
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
        <label for={f.kind === "secret" && !api ? undefined : fid(f)}>
          {f.label}{#if f.required}<span aria-hidden="true"> *</span>{/if}
        </label>
        {#if f.kind === "secret"}
          {#if api}
            <SecretPicker
              {api}
              id={fid(f)}
              bind:value={values[f.name] as string}
              required={f.required}
              invalid={!!errors[f.name]}
              describedby={describe(f)}
            />
          {:else}
            <p class="muted" id={fid(f)}>A secret parameter needs the secret picker.</p>
          {/if}
        {:else if f.kind === "enum"}
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
        {:else if f.kind === "yaml"}
          <textarea
            class="mono yaml"
            id={fid(f)}
            bind:value={values[f.name]}
            readonly={ro(f)}
            spellcheck="false"
            required={f.required}
            aria-invalid={!!errors[f.name]}
            aria-describedby={describe(f)}></textarea>
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
      {#if f.description || ro(f) || f.kind === "secret" || f.kind === "yaml"}
        <span class="hint" id="{fid(f)}-hint">{hintText(f)}</span>
      {/if}
      {#if notes[f.name]}
        <span class="hint note" id="{fid(f)}-note">{notes[f.name]}</span>
      {/if}
      {#if errors[f.name]}
        <span class="error" id="{fid(f)}-error">{errors[f.name]}</span>
      {/if}
    </div>
  {/each}
</div>

<style>
  .nested {
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin: 0;
  }
  .nested legend {
    font-weight: 600;
    font-size: var(--text-sm);
    padding: 0 var(--space-1);
  }
  .yaml {
    min-height: 6rem;
  }
  .note {
    color: var(--color-warn);
  }
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
