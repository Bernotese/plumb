<script lang="ts">
  import { resourceTypes, usersOf } from "../lib/blocks";
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { BlockView, ImageView, TargetView } from "../lib/types";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let data = $state<{ blocks: BlockView[]; targets: TargetView[]; images: ImageView[] } | null>(
    null,
  );
  let error = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => Promise.all([api.listBlocks(), api.listTargets(), api.listImages()]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([blocks, targets, images]) =>
        (data = {
          blocks: blocks.sort((a, b) => a.document.name.localeCompare(b.document.name)),
          targets,
          images,
        }),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Blocks</h1>
  <a class="btn primary" href={href({ name: "block-new" })}>New block</a>
</div>
<p class="muted">
  A Block is a reusable set of resources with parameters. Targets and Images use Blocks. A change to
  a Block is a normal plan: it shows every Target that the change touches.
</p>
<ProblemBox {error} />
{#if data === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if data && data.blocks.length === 0}
  <p class="muted">No blocks yet.</p>
{:else if data}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Blocks, scrolls sideways"
  >
    <table class="stack">
      <caption class="sr-only">Blocks</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Resources</th>
          <th scope="col">Used by</th>
          <th scope="col">Revision</th>
        </tr>
      </thead>
      <tbody>
        {#each data.blocks as b (b.document.name)}
          {@const users = usersOf(b.document.name, data.targets, data.images)}
          <tr>
            <th scope="row">
              <a class="mono" href={href({ name: "block", block: b.document.name })}
                >{b.document.name}</a
              >
              {#if b.document.spec.title}<span class="sub">{b.document.spec.title}</span>{/if}
            </th>
            <td data-label="Resources"
              ><div>
                {plural(b.document.spec.resources.length, "resource")}
                <span class="sub mono">{resourceTypes(b.document).join(", ")}</span>
              </div></td
            >
            <td data-label="Used by"
              ><div>
                {#if users.targets.length + users.images.length === 0}
                  <span class="muted">nothing</span>
                {:else}
                  {[
                    users.targets.length ? plural(users.targets.length, "target") : "",
                    users.images.length ? plural(users.images.length, "image") : "",
                  ]
                    .filter(Boolean)
                    .join(", ")}
                {/if}
              </div></td
            >
            <td data-label="Revision">
              <a
                href={href({ name: "revision", id: b.revision })}
                aria-label="Revision {b.revision}">{b.revision}</a
              >
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
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
</style>
