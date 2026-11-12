<script lang="ts">
  import { paramRows, usersOf, type Users } from "../lib/blocks";
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { BlockView } from "../lib/types";
  import { toYaml } from "../lib/yaml";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, name }: { api: PlumbApi; name: string } = $props();
  let block = $state<BlockView | null>(null);
  let users = $state<Users | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => Promise.all([api.getBlock(name), api.listTargets(), api.listImages()]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([b, t, i]) => {
        block = b;
        users = usersOf(name, t, i);
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const doc = $derived(block?.document);
  const params = $derived(doc ? paramRows(doc) : []);
  const lines = $derived(doc ? toYaml(doc).replace(/\n$/, "").split("\n") : []);
</script>

<p class="back"><a href={href({ name: "blocks" })}>← Blocks</a></p>
<div class="spread">
  <h1 id="page-title" tabindex="-1">Block <span class="mono">{name}</span></h1>
  {#if doc}<a class="btn primary" href={href({ name: "block-edit", block: name })}>Edit</a>{/if}
</div>
<ProblemBox {error} />
{#if doc && block && users}
  {#if doc.spec.title}<p class="lead">{doc.spec.title}</p>{/if}
  {#if doc.spec.description}<p class="pre-line">{doc.spec.description}</p>{/if}
  <p class="muted">
    Last changed in <a href={href({ name: "revision", id: block.revision })}
      >revision {block.revision}</a
    >.
  </p>

  <section aria-labelledby="used-title">
    <h2 id="used-title">Used by</h2>
    {#if users.targets.length + users.images.length === 0}
      <p class="muted">No Target or Image uses this Block.</p>
    {:else}
      <ul class="users">
        {#each users.targets as t (t)}
          <li>
            <Badge tone="muted" label="Target" />
            <a class="mono" href={href({ name: "target-edit", target: t })}>{t}</a>
          </li>
        {/each}
        {#each users.images as i (i)}
          <li>
            <Badge tone="info" label="Image" />
            <a class="mono" href={href({ name: "image", image: i })}>{i}</a>
          </li>
        {/each}
      </ul>
      <p class="muted">A change to this Block is planned on each of these.</p>
    {/if}
  </section>

  <section aria-labelledby="params-title">
    <h2 id="params-title">Parameters</h2>
    {#if params.length === 0}
      <p class="muted">This Block has no parameters.</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div
        class="table-wrap card flush"
        tabindex="0"
        role="region"
        aria-label="Parameters, scrolls sideways"
      >
        <table class="stack">
          <caption class="sr-only">Parameters of {name}</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Type</th>
              <th scope="col">Required</th>
              <th scope="col">Default</th>
            </tr>
          </thead>
          <tbody>
            {#each params as p (p.name)}
              <tr>
                <th scope="row">
                  <span class="mono">{p.name}</span>
                  {#if p.title}<span class="sub">{p.title}</span>{/if}
                </th>
                <td data-label="Type">
                  <span class="mono">{p.type}</span>
                  {#if p.secret}<Badge tone="warn" label="secret" />{/if}
                </td>
                <td data-label="Required">{p.required ? "yes" : "no"}</td>
                <td data-label="Default" class="mono">{p.default ?? "–"}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <section aria-labelledby="yaml-title">
    <h2 id="yaml-title">Document</h2>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="card code" tabindex="0" role="region" aria-label="YAML of {name}, scrolls">
      <ol class="mono">
        {#each lines as l, i (i)}<li>{l || " "}</li>{/each}
      </ol>
    </div>
  </section>
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .lead {
    font-size: var(--text-lg);
    margin: 0 0 var(--space-2);
  }
  .pre-line {
    white-space: pre-line;
  }
  .users {
    list-style: none;
    padding: 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
  }
  .users li {
    display: flex;
    gap: var(--space-2);
    align-items: center;
  }
  .sub {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    font-weight: normal;
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
  .code {
    overflow: auto;
    max-height: 40rem;
    padding: var(--space-3) 0;
  }
  .code ol {
    margin: 0;
    padding-left: 4.5em;
    font-size: var(--text-sm);
    line-height: 1.5;
  }
  .code li {
    white-space: pre;
    padding-right: var(--space-4);
  }
  .code li::marker {
    color: var(--color-text-muted);
  }
</style>
