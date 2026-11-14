<script lang="ts">
  // An object with only scalar `additionalProperties` as key/value rows
  // (D-363). The keys are the object's keys; the values are typed by the
  // value schema when the form saves.
  import { tick } from "svelte";
  import { newRow, type Field, type Row } from "../lib/schemaForm";

  let {
    field,
    rows = $bindable(),
    prefix,
    errors = {},
    readonly = false,
  }: {
    field: Field;
    rows: Row[];
    prefix: string;
    /** Errors with paths relative to this map: `0/name`, `0/value`. */
    errors?: Record<string, string>;
    readonly?: boolean;
  } = $props();

  const valueKind = $derived(field.value?.kind ?? "text");

  async function add() {
    rows = [...rows, newRow(field)];
    await tick();
    document.getElementById(`${prefix}-${rows.length - 1}-name`)?.focus();
  }
  async function remove(i: number) {
    rows = rows.filter((_, j) => j !== i);
    await tick();
    document.getElementById(`${prefix}-add`)?.focus();
  }
  const err = (i: number, part: "name" | "value") => errors[`${i}/${part}`];
  const described = (i: number, part: "name" | "value") =>
    err(i, part) ? `${prefix}-${i}-${part}-error` : undefined;
</script>

<div class="kv" id={prefix}>
  {#if rows.length}
    <table class="rows">
      <caption class="sr-only">{field.label}</caption>
      <thead>
        <tr>
          <th scope="col">Key</th>
          <th scope="col">Value</th>
          <th scope="col"><span class="sr-only">Action</span></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as r, i (r.key)}
          <tr>
            <td>
              <input
                type="text"
                id="{prefix}-{i}-name"
                aria-label="{field.label}: key {i + 1}"
                bind:value={r.name}
                {readonly}
                aria-invalid={!!err(i, "name")}
                aria-describedby={described(i, "name")}
              />
              {#if err(i, "name")}<span class="error" id="{prefix}-{i}-name-error"
                  >{err(i, "name")}</span
                >{/if}
            </td>
            <td>
              {#if valueKind === "boolean"}
                <input
                  type="checkbox"
                  id="{prefix}-{i}-value"
                  aria-label="{field.label}: value {i + 1}"
                  bind:checked={r.value as boolean}
                  disabled={readonly}
                />
              {:else}
                <input
                  type="text"
                  inputmode={valueKind === "integer"
                    ? "numeric"
                    : valueKind === "number"
                      ? "decimal"
                      : undefined}
                  id="{prefix}-{i}-value"
                  aria-label="{field.label}: value {i + 1}"
                  bind:value={r.value as string}
                  {readonly}
                  aria-invalid={!!err(i, "value")}
                  aria-describedby={described(i, "value")}
                />
              {/if}
              {#if err(i, "value")}<span class="error" id="{prefix}-{i}-value-error"
                  >{err(i, "value")}</span
                >{/if}
            </td>
            <td>
              {#if !readonly}
                <button
                  class="btn small danger"
                  type="button"
                  aria-label="Remove {field.label} key {r.name || i + 1}"
                  onclick={() => remove(i)}>Remove</button
                >
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else}
    <p class="muted">No entries.</p>
  {/if}
  {#if !readonly}
    <button class="btn" type="button" id="{prefix}-add" onclick={add}>Add a key</button>
  {/if}
</div>

<style>
  .btn.small {
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
  }
  .rows {
    width: 100%;
    border-collapse: collapse;
    margin-bottom: var(--space-2);
  }
  th {
    text-align: left;
    font-size: var(--text-sm);
    color: var(--color-text-muted);
    padding: 0 var(--space-2) var(--space-1) 0;
  }
  td {
    padding: 0 var(--space-2) var(--space-2) 0;
    vertical-align: top;
  }
  td input[type="text"] {
    width: 100%;
  }
  .error {
    display: block;
  }
</style>
