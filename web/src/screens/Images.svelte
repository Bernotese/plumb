<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import { newestReady, TRIGGER_TEXT } from "../lib/images";
  import { liveSnapshot } from "../lib/live";
  import { href } from "../lib/router";
  import type { ImageView } from "../lib/types";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let images = $state<ImageView[] | null>(null);
  let error = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listImages(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (list) =>
        (images = list.sort((a, b) => a.document.name.localeCompare(b.document.name))),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "image.version" || e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  /** The file name of the base image, without the long address. */
  const baseName = (url: string) => url.split("/").pop() || url;
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Images</h1>
  <a class="btn primary" href={href({ name: "image-new" })}>New image</a>
</div>
<p class="muted">
  A stock image is the cloud image as it is. A built image is a template that Plumb makes from the
  base image plus Blocks, with fresh packages.
</p>
<ProblemBox {error} />
{#if images === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if images && images.length === 0}
  <p class="muted">No images yet.</p>
{:else if images}
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div
    class="table-wrap card flush"
    tabindex="0"
    role="region"
    aria-label="Images, scrolls sideways"
  >
    <table>
      <caption class="sr-only">Images</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Base</th>
          <th scope="col">Kind</th>
          <th scope="col">Newest version</th>
          <th scope="col">Last build</th>
          <th scope="col">Next build</th>
        </tr>
      </thead>
      <tbody>
        {#each images as i (i.document.name)}
          {@const spec = i.document.spec}
          {@const st = i.status}
          {@const newest = newestReady(st)}
          <tr>
            <th scope="row">
              <a class="mono" href={href({ name: "image", image: i.document.name })}
                >{i.document.name}</a
              >
              {#if spec.title}<span class="sub">{spec.title}</span>{/if}
            </th>
            <td class="mono base" title={spec.url}>{baseName(spec.url)}</td>
            <td>
              {#if spec.build}<Badge tone="info" label="Built" />{:else}<Badge
                  tone="muted"
                  label="Stock"
                />{/if}
              {#if spec.type === "lxc"}<span class="sub">Container</span>{/if}
            </td>
            <td>
              {#if !spec.build}–{:else if newest}v{newest.version}{:else}<span class="muted"
                  >none ready</span
                >{/if}
              {#if st?.building}<span class="sub"
                  ><Badge tone="info" label="Building v{st.building}" /></span
                >{/if}
            </td>
            <td>
              {#if st?.last_build}
                {@const lb = st.last_build}
                <a href={href({ name: "run", id: lb.run_id })}>v{lb.version}</a>
                {#if lb.status === "failed"}<Badge tone="danger" label="failed" />{/if}
                <span class="sub"
                  >{formatTime(lb.finished_at ?? lb.started_at)} · {TRIGGER_TEXT[lb.trigger]}</span
                >
              {:else}–{/if}
            </td>
            <td class="nowrap">{st?.next_build_at ? formatTime(st.next_build_at) : "–"}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <p class="muted">Times are local. A schedule is set in UTC.</p>
{/if}

<style>
  .sub {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    font-weight: normal;
  }
  td.base {
    max-width: 16rem;
    white-space: normal;
    word-break: break-all;
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
</style>
