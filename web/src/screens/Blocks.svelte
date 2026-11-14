<script lang="ts">
  import { resourceTypes, usersOf } from "../lib/blocks";
  import type { CatalogBlock, PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import { go, href } from "../lib/router";
  import { can, roleNote } from "../lib/session.svelte";
  import type { BlockView, ImageView, TargetView } from "../lib/types";
  import Badge from "../ui/Badge.svelte";
  import DocDiff from "../ui/DocDiff.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let data = $state<{ blocks: BlockView[]; targets: TargetView[]; images: ImageView[] } | null>(
    null,
  );
  let error = $state<unknown>(null);
  let catalog = $state<CatalogBlock[] | null>(null);
  let catalogError = $state<unknown>(null);
  let busy = $state<string | null>(null);
  let actionError = $state<{ name: string; error: unknown } | null>(null);
  let diffOpen = $state<string | null>(null);
  const mayPropose = $derived(can("createPlan"));

  $effect(() => {
    const live = liveSnapshot({
      load: () =>
        Promise.all([
          api.listBlocks(),
          api.listTargets(),
          api.listImages(),
          // An older server has no catalogue: the list still shows.
          api.listCatalogBlocks().then(
            (c) => ({ ok: true as const, c }),
            (e: unknown) => ({ ok: false as const, e }),
          ),
        ]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([blocks, targets, images, cat]) => {
        data = {
          blocks: blocks.sort((a, b) => a.document.name.localeCompare(b.document.name)),
          targets,
          images,
        };
        if (cat.ok) {
          catalog = cat.c;
          catalogError = null;
        } else catalogError = cat.e;
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

  /** Add or update a Block from the catalogue: a normal proposal (D-328). */
  async function propose(c: CatalogBlock) {
    busy = c.name;
    actionError = null;
    try {
      const plan = await api.createPlan({
        reason:
          c.stored === "none"
            ? `Add the starter Block ${c.name} from the catalogue.`
            : `Update the Block ${c.name} from the catalogue.`,
        put: [c.document],
      });
      go({ name: "plan", id: plan.id });
    } catch (e) {
      actionError = { name: c.name, error: e };
    } finally {
      busy = null;
    }
  }
  const storedText = {
    none: { label: "not added", tone: "muted" },
    same: { label: "added, same", tone: "ok" },
    differs: { label: "added, differs", tone: "warn" },
  } as const;
</script>

<div class="spread">
  <h1 id="page-title" tabindex="-1">Blocks</h1>
  {#if mayPropose}<a class="btn primary" href={href({ name: "block-new" })}>New block</a>{/if}
</div>
<p class="muted">
  A Block is a reusable set of resources with parameters. Targets and Images use Blocks. A change to
  a Block is a normal plan: it shows every Target that the change touches.
</p>
<ProblemBox {error} />
{#if data === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if data && data.blocks.length === 0}
  <p class="muted">
    No blocks yet. Add the ones you need from the <a href="#catalog-title">starter catalogue</a> below.
  </p>
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

<section aria-labelledby="catalog-title" class="catalog">
  <h2 id="catalog-title">Starter catalogue</h2>
  <p class="muted">
    Blocks that ship with this Plumb. Plumb never adds or changes one by itself: "Add" and "Update"
    make a normal plan that you approve.
  </p>
  {#if !mayPropose}<p class="muted" id="catalog-role">{roleNote("plan")}</p>{/if}
  <ProblemBox error={catalogError} />
  {#if catalog === null && !catalogError}
    <p class="muted" aria-live="polite">Loading…</p>
  {:else if catalog && catalog.length === 0}
    <p class="muted">This Plumb ships no starter Blocks.</p>
  {:else if catalog}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div
      class="table-wrap card flush"
      tabindex="0"
      role="region"
      aria-label="Starter catalogue, scrolls sideways"
    >
      <table class="stack">
        <caption class="sr-only">Starter catalogue</caption>
        <thead>
          <tr>
            <th scope="col">Block</th>
            <th scope="col">Works on</th>
            <th scope="col">Your Blocks</th>
            <th scope="col">Action</th>
          </tr>
        </thead>
        <tbody>
          {#each catalog as c (c.name)}
            {@const st = storedText[c.stored]}
            <tr>
              <th scope="row">
                <span class="mono">{c.name}</span>
                <span class="sub">{c.title}{c.description ? ` · ${c.description}` : ""}</span>
              </th>
              <td data-label="Works on"
                >{c.kinds.map((k) => (k === "vm" ? "VM" : "LXC")).join(", ")}</td
              >
              <td data-label="Your Blocks"
                ><span
                  ><Badge tone={st.tone} label={st.label} />{#if c.stored !== "none"}
                    <a class="sub" href={href({ name: "block", block: c.name })}>open yours</a
                    >{/if}</span
                ></td
              >
              <td data-label="Action">
                <div class="row actions">
                  {#if c.stored === "differs"}
                    <button
                      class="btn"
                      type="button"
                      aria-expanded={diffOpen === c.name}
                      aria-controls="catalog-diff-{c.name}"
                      onclick={() => (diffOpen = diffOpen === c.name ? null : c.name)}
                      >{diffOpen === c.name ? "Hide difference" : "Show difference"}</button
                    >
                  {/if}
                  {#if mayPropose && c.stored !== "same"}
                    <button
                      class="btn {c.stored === 'none' ? 'primary' : ''}"
                      type="button"
                      aria-label="{c.stored === 'none'
                        ? 'Add'
                        : 'Update from the catalogue:'} {c.name}"
                      disabled={busy !== null}
                      onclick={() => propose(c)}
                      >{c.stored === "none" ? "Add" : "Update from the catalogue"}</button
                    >
                  {/if}
                  {#if c.stored === "same"}<span class="muted">–</span>{/if}
                </div>
                {#if actionError?.name === c.name}<ProblemBox error={actionError.error} />{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#each catalog as c (c.name)}
      {#if diffOpen === c.name && data}
        {@const mine = data.blocks.find((b) => b.document.name === c.name)}
        <div id="catalog-diff-{c.name}">
          <p class="muted">Yours (−) and the catalogue (+). An update replaces yours.</p>
          <DocDiff
            id="catalog-doc-{c.name}"
            change={{
              kind: "Block",
              name: c.name,
              op: "changed",
              before: mine?.document ?? null,
              after: c.document,
            }}
          />
        </div>
      {/if}
    {/each}
  {/if}
</section>

<style>
  .catalog {
    margin-top: var(--space-6);
  }
  .actions {
    flex-wrap: wrap;
  }
  .actions .btn {
    white-space: nowrap;
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
</style>
