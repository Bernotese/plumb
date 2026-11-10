<script lang="ts">
  let {
    id,
    label,
    items = $bindable(),
    readonly = false,
    describedby,
    invalid = false,
  }: {
    id: string;
    label: string;
    items: string[];
    readonly?: boolean;
    describedby?: string;
    invalid?: boolean;
  } = $props();

  function add() {
    items = [...items, ""];
    queueMicrotask(() => document.getElementById(`${id}-${items.length - 1}`)?.focus());
  }
  function remove(i: number) {
    items = items.filter((_, j) => j !== i);
    queueMicrotask(() => document.getElementById(`${id}-add`)?.focus());
  }
</script>

<div class="list" role="group" aria-labelledby="{id}-label" aria-describedby={describedby}>
  {#each Array.from(items.keys()) as i (i)}
    <div class="row item">
      <input
        type="text"
        id="{id}-{i}"
        aria-label="{label}, entry {i + 1}"
        bind:value={items[i]}
        {readonly}
        aria-invalid={invalid}
      />
      {#if !readonly}
        <button
          type="button"
          class="btn"
          onclick={() => remove(i)}
          aria-label="Remove {label} entry {i + 1}">Remove</button
        >
      {/if}
    </div>
  {:else}
    <p class="muted empty">No entries.</p>
  {/each}
  {#if !readonly}
    <button type="button" class="btn" id="{id}-add" onclick={add}>Add entry</button>
  {/if}
</div>

<style>
  .list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    align-items: flex-start;
  }
  .item input {
    min-width: 18rem;
  }
  .empty {
    margin: 0;
    font-size: var(--text-sm);
  }
</style>
