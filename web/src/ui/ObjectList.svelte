<script lang="ts">
  // An array of objects as a list of nested forms (D-363): add, remove and
  // move items. Each item keeps the stored keys the form cannot show.
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { newItem, type Field, type Item, type Values } from "../lib/schemaForm";
  import SchemaFields from "./SchemaFields.svelte";

  let {
    field,
    items = $bindable(),
    prefix,
    errors = {},
    api,
    readonly = false,
  }: {
    field: Field;
    items: Item[];
    prefix: string;
    /** Errors with paths relative to this list: `0/host`. */
    errors?: Record<string, string>;
    api?: PlumbApi;
    readonly?: boolean;
  } = $props();

  const sub = (i: number) =>
    Object.fromEntries(
      Object.entries(errors)
        .filter(([k]) => k.startsWith(`${i}/`))
        .map(([k, v]) => [k.slice(`${i}/`.length), v]),
    );
  const full = $derived(field.maxItems !== undefined && items.length >= field.maxItems);

  async function focus(id: string) {
    await tick();
    document.getElementById(id)?.focus();
  }

  function add() {
    items = [...items, newItem(field)];
    void focus(`${prefix}-${items.length - 1}-legend`);
  }
  function remove(i: number) {
    items = items.filter((_, j) => j !== i);
    void focus(
      items.length ? `${prefix}-${Math.min(i, items.length - 1)}-legend` : `${prefix}-add`,
    );
  }
  function move(i: number, by: -1 | 1) {
    const j = i + by;
    if (j < 0 || j >= items.length) return;
    const next = items.slice();
    [next[i], next[j]] = [next[j] as Item, next[i] as Item];
    items = next;
    // The same button again, unless the item reached the end where it is disabled.
    const edge = by < 0 ? j === 0 : j === items.length - 1;
    void focus(`${prefix}-${j}-${edge ? "legend" : by < 0 ? "up" : "down"}`);
  }
</script>

<div class="objects" id={prefix}>
  {#each items as it, i (it.key)}
    <fieldset class="item" aria-labelledby="{prefix}-{i}-legend">
      <div class="spread">
        <legend id="{prefix}-{i}-legend" tabindex="-1">{field.label} {i + 1}</legend>
        {#if !readonly}
          <div class="row actions">
            <button
              class="btn small"
              type="button"
              id="{prefix}-{i}-up"
              disabled={i === 0}
              aria-label="Move {field.label} {i + 1} up"
              onclick={() => move(i, -1)}>Move up</button
            >
            <button
              class="btn small"
              type="button"
              id="{prefix}-{i}-down"
              disabled={i === items.length - 1}
              aria-label="Move {field.label} {i + 1} down"
              onclick={() => move(i, 1)}>Move down</button
            >
            <button
              class="btn small danger"
              type="button"
              aria-label="Remove {field.label} {i + 1}"
              onclick={() => remove(i)}>Remove</button
            >
          </div>
        {/if}
      </div>
      <SchemaFields
        fields={field.fields ?? []}
        bind:values={it.values as Values}
        errors={sub(i)}
        prefix="{prefix}-{i}"
        {api}
        readonly={readonly ? (field.fields ?? []).map((f) => f.name) : []}
      />
    </fieldset>
  {:else}
    <p class="muted">No entries.</p>
  {/each}
  {#if !readonly}
    <button class="btn" type="button" id="{prefix}-add" disabled={full} onclick={add}
      >Add {field.label.toLowerCase()}</button
    >
    {#if full}<span class="hint">At most {field.maxItems} entries.</span>{/if}
  {/if}
</div>

<style>
  .btn.small {
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
  }
  .item {
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin: 0 0 var(--space-3);
  }
  legend {
    font-weight: 600;
    float: left;
    padding: 0;
  }
  .spread {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
    margin-bottom: var(--space-2);
  }
  .actions {
    gap: var(--space-1);
  }
  .item :global(.grid) {
    clear: both;
  }
</style>
