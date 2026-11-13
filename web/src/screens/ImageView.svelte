<script lang="ts">
  import type { PlumbApi } from "../lib/client";
  import {
    TRIGGER_TEXT,
    bytesText,
    diskUse,
    newestReady,
    scheduleText,
    type Trigger,
    safeFreeCommand,
    basePin,
  } from "../lib/images";
  import { liveSnapshot } from "../lib/live";
  import { go, href } from "../lib/router";
  import type { ImageStatus, ImageVersion, ImageView } from "../lib/types";
  import { KIND_NAME, formatTime, type Tone } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import DeleteDocument from "../ui/DeleteDocument.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import { restored, RESTORED_WAIT } from "../lib/restored.svelte";

  let { api, name }: { api: PlumbApi; name: string } = $props();
  let image = $state<ImageView | null>(null);
  /** Targets that use this Image: a delete cannot work while there are any. */
  let usedBy = $state<string[] | null>(null);
  let error = $state<unknown>(null);
  let buildError = $state<unknown>(null);
  let busy = $state(false);
  let reason = $state("");
  /** Why a version failed: the output of its failed step, by version. */
  let failures = $state<Record<number, string>>({});
  let copied = $state("");

  $effect(() => {
    const live = liveSnapshot({
      load: () => Promise.all([api.getImage(name), api.listTargets()]),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: ([i, t]) => {
        image = i;
        usedBy = t
          .filter((x) => x.document.spec.image === name)
          .map((x) => x.document.name)
          .sort();
      },
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.subject === `image:${name}` || e.type === "revision.committed") l.reloadSoon();
      },
    });
    return () => live.stop();
  });

  // The reason of a failed build is in its Run (answer 7: the disk check).
  $effect(() => {
    for (const v of image?.status?.versions ?? []) {
      if (v.status !== "failed" || v.version in failures) continue;
      failures = { ...failures, [v.version]: "" };
      api.getRun(v.run_id).then(
        (run) => {
          const step = run.images
            ?.find((b) => b.image === name && b.version === v.version)
            ?.steps.find((s) => s.status === "failed");
          failures = {
            ...failures,
            [v.version]: step ? `${step.resource}: ${step.output || "failed"}` : "",
          };
        },
        () => {},
      );
    }
  });

  const spec = $derived(image?.document.spec);
  const lxc = $derived(spec?.type === "lxc");
  const st = $derived(image?.status);
  const newest = $derived(newestReady(st));
  const use = $derived(st?.disk ? diskUse(st.disk) : null);

  const stateTone: Record<ImageVersion["status"], Tone> = {
    building: "info",
    ready: "ok",
    failed: "danger",
    removed: "muted",
  };
  const triggerText = (t: Trigger) => TRIGGER_TEXT[t];

  async function buildNow(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    buildError = null;
    try {
      const plan = await api.buildImage(name, reason.trim() || undefined);
      go({ name: "plan", id: plan.id });
    } catch (err) {
      buildError = err;
    } finally {
      busy = false;
    }
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      copied = text;
    } catch {
      copied = "";
    }
  }
</script>

{#snippet unusedFiles(st: ImageStatus)}
  <section class="card" aria-labelledby="imports-title">
    {#if lxc}
      <h2 id="imports-title">Unused template files</h2>
    {:else}
      <h2 id="imports-title">Unused import files</h2>
    {/if}
    {#if st.unused_imports.length === 0}
      <p class="muted">
        None. {lxc ? "Every template file is in use." : "Every downloaded base image is in use."}
      </p>
    {:else}
      <p>
        {#if lxc}
          These template files of an old checksum or controller address are not needed any more: a
          container keeps no link to its template. Plumb never deletes a template file (D-233): its
          API token has no right to delete files on the image storage, and that right would also let
          it change the storage itself. To free the space, run the command on the Proxmox host.
        {:else}
          These downloads of an old base image are not needed any more. Safe to remove: no version
          needs it, because a version template is a full copy. Plumb does not delete them: its API
          token has no right to delete files on the image storage, and that right would also let it
          change the storage itself. To free the space, run the command on the Proxmox host.
        {/if}
      </p>
      <ul class="plain imports">
        {#each st.unused_imports as f (f.volume)}
          {@const cmd = safeFreeCommand(f.command)}
          <li>
            <span class="mono wrap">{f.volume}</span>
            <span class="muted">({bytesText(f.size_bytes)})</span>
            {#if cmd}
              <div class="cmd">
                <code class="mono">{cmd}</code>
                <button class="btn" type="button" onclick={() => copy(cmd)}
                  >Copy<span class="sr-only"> the command for {f.volume}</span></button
                >
              </div>
            {:else}
              <p class="warn">
                This file does not have the name form of Plumb. Check it by hand on the Proxmox host
                before you remove it.
              </p>
            {/if}
          </li>
        {/each}
      </ul>
      <p class="sr-only" aria-live="polite">{copied ? "Command copied." : ""}</p>
    {/if}
  </section>
{/snippet}

<div class="spread">
  <h1 id="page-title" tabindex="-1">Image {name}</h1>
  {#if image}<a class="btn" href={href({ name: "image-edit", image: name })}>Edit</a>{/if}
</div>
<ProblemBox {error} />
{#if image && spec}
  <div class="card">
    <dl class="facts">
      {#if spec.title}<dt>Title</dt>
        <dd>{spec.title}</dd>{/if}
      <dt>Type</dt>
      <dd id="image-type">{KIND_NAME[spec.type]}</dd>
      <dt>Kind</dt>
      <dd>
        {#if spec.type === "lxc" && spec.build}<Badge tone="info" label="Built" /> Plumb makes container
          templates from the Proxmox template.
        {:else if spec.type === "lxc"}<Badge tone="muted" label="Stock" /> Plumb adds the agent installer
          to the Proxmox template.
        {:else if spec.build}<Badge tone="info" label="Built" /> Plumb makes templates from the base image.
        {:else}<Badge tone="muted" label="Stock" /> Plumb imports the base image as it is.{/if}
      </dd>
      <dt>Base image</dt>
      <dd class="mono wrap">{spec.url}</dd>
      <dt>Checksum</dt>
      <dd class="mono wrap" id="image-sum">
        {spec.type === "lxc" ? `sha512 ${spec.sha512 ?? "–"}` : `sha256 ${spec.sha256 ?? "–"}`}
      </dd>
    </dl>
  </div>

  {#if spec.build && st}
    {@const b = spec.build}
    {#if st.last_build?.status === "failed"}
      <div class="banner warn" role="alert" id="last-failed">
        <strong>The last build failed</strong> (v{st.last_build.version},
        <a href={href({ name: "run", id: st.last_build.run_id })}>Run {st.last_build.run_id}</a>).
        {failures[st.last_build.version] ?? ""} "Build now" tries again. A scheduled build does not try
        again before its next slot.
      </div>
    {/if}

    <section class="card" aria-labelledby="build-title">
      <h2 id="build-title">Build</h2>
      {#if st.building}
        <p id="building">
          <Badge tone="info" label="Building v{st.building}" />
          {#each st.versions.filter((v) => v.version === st.building) as v (v.version)}
            <a href={href({ name: "run", id: v.run_id })}>Watch run {v.run_id}</a>
          {/each}
        </p>
        <p class="muted">One build per image at a time. "Build now" is open again when it ends.</p>
      {/if}
      <p>
        Newest ready version: {newest ? `v${newest.version}` : "none"}. New Targets get it. A new
        version changes no existing Target.
      </p>
      <form onsubmit={buildNow}>
        <div class="field">
          <label for="build-reason">Reason (optional)</label>
          <input id="build-reason" type="text" bind:value={reason} maxlength="2000" />
        </div>
        <ProblemBox error={buildError} />
        <button
          class="btn primary"
          type="submit"
          disabled={busy || st.building !== null || restored.mode}
          aria-describedby={restored.mode ? "build-restored" : undefined}>Build now</button
        >
        {#if restored.mode}<p class="muted" id="build-restored">{RESTORED_WAIT}</p>{/if}
        <p class="muted">
          This makes a plan for a new version from the recipe below. Nothing happens until it is
          approved.
        </p>
      </form>
      {#if use && st.disk}
        <p class:warn={use.now > st.disk.limit_percent} id="disk-use">
          Disk storage {use.text} A build does not start when the storage would be more than {st
            .disk.limit_percent} % full with the whole build disk ({b.guest.disk} GiB).
        </p>
        <meter
          min="0"
          max="100"
          low={st.disk.limit_percent - 10}
          high={st.disk.limit_percent}
          optimum="0"
          value={use.now}
          aria-labelledby="disk-use">{use.now} %</meter
        >
      {/if}
    </section>

    <section class="card" aria-labelledby="recipe-title">
      <h2 id="recipe-title">Recipe</h2>
      <dl class="facts">
        <dt>Blocks</dt>
        <dd>
          {#if b.blocks?.length}
            <ul class="plain">
              {#each b.blocks as u, i (i)}
                <li>
                  <span class="mono">{u.use}</span>{#if u.id && u.id !== u.use}
                    <span class="muted">as {u.id}</span>{/if}
                  {#if u.params && Object.keys(u.params).length}
                    <span class="mono muted">{JSON.stringify(u.params)}</span>
                  {/if}
                </li>
              {/each}
            </ul>
          {:else}none: the base image with fresh packages{/if}
        </dd>
        <dt>Upgrade</dt>
        <dd>
          {b.upgrade === false
            ? "no"
            : lxc
              ? "yes: apt-get dist-upgrade runs in the build container before the template is made"
              : "yes: all packages are upgraded in the build"}
        </dd>
        <dt>{lxc ? "Build container" : "Build guest"}</dt>
        <dd>
          {b.guest.cores ?? 1} CPU · {b.guest.memory ?? 1024} MiB · {b.guest.disk} GiB disk ·
          <span class="mono">{b.guest.bridge}</span> · {b.guest.ip ?? "dhcp"}
          <span class="sub">The disk is the smallest disk of a Target from this image.</span>
        </dd>
        <dt>Keep</dt>
        <dd>{spec.retention?.keep ?? 3} newest ready versions</dd>
        <dt>Schedule</dt>
        <dd>
          {scheduleText(spec.schedule)}{#if st.next_build_at}
            <span class="sub">Next build: {formatTime(st.next_build_at)} (local time)</span>{/if}
        </dd>
      </dl>
    </section>

    <h2>Versions</h2>
    {#if st.versions.length === 0}
      <p class="muted">No version yet. Use "Build now".</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div
        class="table-wrap card flush"
        tabindex="0"
        role="region"
        aria-label="Versions of image {name}, scrolls sideways"
      >
        <table>
          <caption class="sr-only">Versions of image {name}</caption>
          <thead>
            <tr>
              <th scope="col">Version</th>
              <th scope="col">State</th>
              <th scope="col">Built</th>
              <th scope="col">Run</th>
              <th scope="col">Targets from it</th>
            </tr>
          </thead>
          <tbody>
            {#each st.versions as v (v.version)}
              <tr>
                <th scope="row">v{v.version}</th>
                <td>
                  <Badge tone={stateTone[v.status]} label={v.status} />
                  {#if v.status === "failed" && failures[v.version]}
                    <span class="sub reason">{failures[v.version]}</span>
                  {/if}
                  {#if v.removed_at}<span class="sub">removed {formatTime(v.removed_at)}</span>{/if}
                </td>
                <td>
                  {formatTime(v.finished_at ?? v.started_at)}
                  <span class="sub">{triggerText(v.trigger)} · revision {v.revision}</span>
                  {#if basePin(v.base_sha256)}
                    {@const p = basePin(v.base_sha256)}
                    <span class="sub mono base-pin" title={v.base_sha256}
                      >base {p?.algo} {p?.short}…</span
                    >
                  {/if}
                </td>
                <td><a href={href({ name: "run", id: v.run_id })}>Run {v.run_id}</a></td>
                <td>
                  {#each v.targets as t, i (t)}{i ? ", " : ""}<a
                      class="mono"
                      href={href({ name: "target-edit", target: t })}>{t}</a
                    >{:else}–{/each}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}

    {#if st.left_templates?.length}
      <section class="card" aria-labelledby="left-title">
        <h2 id="left-title">Left templates</h2>
        <p>
          A build converted these guests to templates but did not finish. No version uses them, and
          Plumb never uses or removes them. Remove each one by hand in Proxmox.
        </p>
        <ul class="plain imports">
          {#each st.left_templates as l (l.guest_id)}
            <li>
              <span class="mono wrap">{l.name}</span>
              <span class="muted">(VMID <span class="mono">{l.guest_id}</span>)</span>
              <p class="warn">left by a build; remove it by hand</p>
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {@render unusedFiles(st)}
  {:else if spec.type === "lxc"}
    {#if st}
      {@render unusedFiles(st)}
    {/if}
    <p class="muted" id="lxc-no-versions">
      A stock container image has no versions: each create uses the Proxmox template with the agent
      installer added. Edit it and switch on "Build" to make container templates with Blocks and
      fresh packages.
    </p>
  {:else if spec.build}
    <p class="muted">The server does not show builds yet.</p>
  {:else}
    <p class="muted">
      A stock image has no versions. Edit it and switch on "Build" to make templates with Blocks and
      fresh packages.
    </p>
  {/if}
{/if}
{#if image && usedBy}
  <DeleteDocument
    {api}
    kind="Image"
    {name}
    blocked={usedBy.length
      ? `Used by ${usedBy.join(", ")}. Delete those Targets or move them to another Image first.`
      : undefined}
  >
    <p>
      The plan removes the Image from the desired state.{image.document.spec.build
        ? " The plan lists the templates in Proxmox that go with it."
        : ""} No Target changes: none uses it.
    </p>
  </DeleteDocument>
{:else if !image && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .facts {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }
  .facts dt {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  .facts dd {
    margin: 0;
  }
  @media (max-width: 600px) {
    .facts {
      grid-template-columns: minmax(0, 1fr);
      gap: 0;
    }
    .facts dd {
      margin-bottom: var(--space-2);
    }
  }
  .sub {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  .reason {
    color: var(--color-danger);
    max-width: 32rem;
  }
  .wrap {
    overflow-wrap: anywhere;
  }
  .plain {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .imports li {
    margin-bottom: var(--space-3);
  }
  .cmd {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
    margin-top: var(--space-1);
  }
  .cmd code {
    padding: var(--space-1) var(--space-2);
    background: var(--color-surface-2);
    border-radius: 4px;
    overflow-wrap: anywhere;
  }
  .warn {
    color: var(--color-warn);
  }
  meter {
    width: 100%;
    max-width: 24rem;
  }
  .flush {
    padding: 0;
    overflow: hidden;
  }
  section h2 {
    margin-top: 0;
  }
</style>
