<script lang="ts">
  // The Image part of a Plan (T-064, D-199, D-209): the new version, the
  // resources of the build guest, the removals, the disk storage.
  import { TRIGGER_TEXT, diskLimitText, diskUse } from "../lib/images";
  import { href } from "../lib/router";
  import type { ImagePlan } from "../lib/types";
  import Badge from "./Badge.svelte";
  import RiskBadge from "./RiskBadge.svelte";

  let {
    part,
    humanOnly = false,
    buildDiskGb,
  }: {
    part: ImagePlan;
    /** The Plan has the human-only reason image-recipe. */
    humanOnly?: boolean;
    /** The disk of the build guest, for the disk check (D-203). */
    buildDiskGb?: number;
  } = $props();

  const use = $derived(part.disk ? diskUse(part.disk, buildDiskGb ?? 0) : null);
</script>

<section class="card image" aria-labelledby="img-{part.image}">
  <div class="spread">
    <h2 id="img-{part.image}">
      Image <a class="mono" href={href({ name: "image", image: part.image })}>{part.image}</a>
    </h2>
    {#if humanOnly}
      <Badge tone="warn" label="A person must approve: image recipe" />
    {/if}
  </div>
  {#if part.build}
    {@const b = part.build}
    <p>
      <strong>New version v{b.version}</strong> ({TRIGGER_TEXT[b.trigger]}).
      {b.upgrade ? "All packages are upgraded in the build." : "Packages are not upgraded."}
      <span class="muted">The Run can give a higher number if another build ends first.</span>
    </p>
    <p class="muted">
      The build makes a new template and changes no guest. A Target takes the newest ready version
      only when it is created.
    </p>
    {#if b.resources.length}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div
        class="table-wrap"
        tabindex="0"
        role="region"
        aria-label="Resources of the build guest of {part.image}, scrolls sideways"
      >
        <table>
          <caption class="sr-only">Resources of the build guest of {part.image}</caption>
          <thead>
            <tr>
              <th scope="col">Resource in the build guest</th>
              <th scope="col">Action</th>
              <th scope="col">Risk</th>
            </tr>
          </thead>
          <tbody>
            {#each b.resources as r (r.resource)}
              <tr>
                <th scope="row" class="mono">{r.resource}</th>
                <td>Create</td>
                <td><RiskBadge risk={r.risk} /></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="muted">
        Not observed before the approval: the build guest does not exist yet. Every resource is
        created in a fresh guest.
      </p>
    {:else}
      <p class="muted">No Blocks: the template is the base image with fresh packages.</p>
    {/if}
  {/if}
  {#if part.removes.length}
    <h3>Removes</h3>
    <ul class="removes">
      {#each part.removes as r (r.name)}
        <li>
          {#if r.kind === "version"}
            Removes v{r.version} <span class="mono muted">({r.name})</span>
          {:else}
            Removes the old base template <span class="mono">{r.name}</span>
          {/if}
        </li>
      {/each}
    </ul>
    <p class="muted">
      {part.build
        ? "Only after the new version is ready. A failed build removes nothing."
        : "When the Run of this plan succeeds."} Guests made from these versions keep running: each is
      a full clone.
    </p>
  {/if}
  {#if part.disk && use}
    <p class:warn={use.over} id="disk-{part.image}">
      {use.text}
      {#if part.build}With this build: {use.after} % (limit {part.disk.limit_percent} %).{/if}
    </p>
    {#if use.over && part.build}
      <p class="banner warn" role="note">
        {diskLimitText(part.disk, buildDiskGb ?? 0)} The Run will fail at the step image:base, and nothing
        is made.
      </p>
    {/if}
  {/if}
  {#each part.notes ?? [] as note (note)}
    <p class="warn" role="note">{note}</p>
  {/each}
</section>

<style>
  .image {
    border-left: 4px solid var(--color-info);
  }
  .image h2 {
    margin-top: 0;
  }
  h3 {
    font-size: var(--text-md);
    margin: var(--space-3) 0 var(--space-1);
  }
  .removes {
    margin: 0 0 var(--space-1);
    padding-left: var(--space-6);
  }
  .warn {
    color: var(--color-warn);
  }
</style>
