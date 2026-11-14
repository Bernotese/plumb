<script lang="ts">
  import { tick } from "svelte";
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import { can, canApprove, me, roleOf } from "../lib/session.svelte";
  import type { DocumentChange, Plan } from "../lib/types";
  import { formatTime, humanOnlyReasons } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ChangeTable from "../ui/ChangeTable.svelte";
  import DocDiff from "../ui/DocDiff.svelte";
  import ImagePlanPart from "../ui/ImagePlanPart.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import { restored, RESTORED_WAIT } from "../lib/restored.svelte";
  import RiskBadge from "../ui/RiskBadge.svelte";

  let { api, id }: { api: PlumbApi; id: number } = $props();
  let plan = $state<Plan | null>(null);
  let error = $state<unknown>(null);
  let approveError = $state<unknown>(null);
  let comment = $state("");
  let busy = $state(false);
  let confirmDrop = $state(false);
  let dropError = $state<unknown>(null);

  async function askDrop() {
    confirmDrop = true;
    dropError = null;
    await tick();
    document.getElementById("drop-confirm")?.focus();
  }

  async function drop() {
    busy = true;
    dropError = null;
    try {
      await api.discardPlan(id);
      confirmDrop = false;
      await load();
      document.getElementById("page-title")?.focus();
    } catch (err) {
      dropError = err;
      if (err instanceof ApiError && err.problem.status === 409) await load();
    } finally {
      busy = false;
    }
  }

  const load = () =>
    api.getPlan(id).then(
      (p) => (plan = p),
      (e: unknown) => (error = e),
    );
  $effect(() => {
    void load();
  });

  // The build disk of each Image of the Plan, for the disk check (D-203).
  let buildDisk = $state<Record<string, number>>({});
  $effect(() => {
    const names = (plan?.images ?? []).filter((i) => i.build).map((i) => i.image);
    for (const n of names) {
      if (n in buildDisk) continue;
      api.getImage(n).then(
        (v) => {
          const d = v.document.spec.build?.guest.disk;
          if (d !== undefined) buildDisk = { ...buildDisk, [n]: d };
        },
        // A new Image is not at the head yet; the Plan shows the use without it.
        () => {},
      );
    }
  });
  // T-168 (host finding F1 of T-158): the document changes that the
  // approval commits, so that no Plan is approved blind. A long list shows
  // the first diffs and the rest on request; an entry past the server bound
  // (T-170) has no text.
  const FIRST_DOCS = 20;
  let allDocs = $state(false);
  const docs = $derived(plan?.documents);
  const docCount = $derived({
    added: docs?.filter((c) => c.op === "added").length ?? 0,
    changed: docs?.filter((c) => c.op === "changed").length ?? 0,
    removed: docs?.filter((c) => c.op === "removed").length ?? 0,
  });
  const shownDocs = $derived(docs && !allDocs ? docs.slice(0, FIRST_DOCS) : (docs ?? []));
  const noText = (c: DocumentChange) =>
    (c.op !== "added" && c.before === null) || (c.op !== "removed" && c.after === null);
  const opLabel = { added: "Added", changed: "Changed", removed: "Removed" } as const;
  const opTone = { added: "ok", changed: "info", removed: "danger" } as const;

  const builds = $derived((plan?.images ?? []).filter((i) => i.build).length);

  const count = $derived(
    plan
      ? plan.targets.flatMap((t) => t.resources).filter((r) => r.action !== "restart").length
      : 0,
  );
  const restarts = $derived(
    plan
      ? plan.targets.flatMap((t) => t.resources).filter((r) => r.action === "restart").length
      : 0,
  );
  const originText: Partial<Record<Plan["origin"], string>> = {
    rollback: "Rollback to an earlier revision",
    import: "Import of an export file",
    ignore_rule: "Ignore rule for an issue",
    accept: "Accept the guest value of an issue",
    revert: "Revert drift of an issue",
    reenroll: "Re-enroll a target",
    build: "Image build (no document change)",
    agent_update: "Agent update (no document change)",
    pin_refresh: "New base checksum of an image (only the pin changes)",
  };
  const pinSource = {
    "ubuntu-cloud": "Ubuntu cloud images (SHA256SUMS)",
    "proxmox-aplinfo": "Proxmox appliance index",
    "debian-https": "Debian SHA512SUMS over HTTPS",
  } as const;
  const actionLabel = {
    create: "Create",
    update: "Change",
    delete: "Delete",
    restart: "Restart",
  } as const;

  async function approve(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    approveError = null;
    try {
      const run = await api.approvePlan(id, comment || undefined);
      go({ name: "run", id: run.id });
    } catch (err) {
      approveError = err;
      if (err instanceof ApiError && err.problem.status === 409) await load();
    } finally {
      busy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">Plan {id}</h1>
<ProblemBox {error} />
{#if plan}
  {#if plan.status === "stale"}
    <div class="banner warn" role="alert">
      <strong>This plan is stale.</strong> It was dropped, or the desired state changed after it was
      made, so it cannot be approved. Make the change again to get a new plan.
      <a href={href({ name: "plans" })}>Pending plans</a>
    </div>
  {:else if plan.status === "approved"}
    <div class="banner ok">
      Approved by <span class="mono">{plan.approved_by}</span> at {formatTime(plan.approved_at)}.
      {#if plan.run_id}<a href={href({ name: "run", id: plan.run_id })}>Watch run {plan.run_id}</a
        >{/if}
    </div>
  {/if}

  <div class="card">
    <dl class="facts">
      <dt>Reason</dt>
      <dd>{plan.reason}</dd>
      <dt>Author</dt>
      <dd class="mono">{plan.author}</dd>
      <dt>Created</dt>
      <dd>{formatTime(plan.created_at)}</dd>
      <dt>Risk of the plan</dt>
      <dd><RiskBadge risk={plan.risk} /></dd>
      <dt>Summary</dt>
      <dd>
        {count} resource change{count === 1 ? "" : "s"}{restarts
          ? `, ${restarts} restart${restarts === 1 ? "" : "s"}`
          : ""}{builds ? `, ${builds} image build${builds === 1 ? "" : "s"}` : ""}{plan.pins?.length
          ? `, ${plan.pins.length} new image checksum${plan.pins.length === 1 ? "" : "s"}`
          : ""}
      </dd>
      {#if plan.origin !== "proposal"}
        <dt>Origin</dt>
        <dd>
          {#if plan.origin === "rollback" && plan.rollback_to}
            Rollback to <a href={href({ name: "revision", id: plan.rollback_to })}
              >revision {plan.rollback_to}</a
            >
          {:else}
            {originText[plan.origin] ?? plan.origin}
          {/if}
        </dd>
      {/if}
      {#if plan.issue_id}
        <dt>Issue</dt>
        <dd>
          <a href={href({ name: "issue", id: plan.issue_id })}>Issue {plan.issue_id}</a>
          ({plan.origin})
        </dd>
      {/if}
    </dl>
  </div>

  {@const why = humanOnlyReasons(plan)}
  {#if why.length && plan.status === "pending"}
    <div class="banner info" role="note">
      <strong>Only a person can approve this plan.</strong> An API token cannot.
      <ul class="why">
        {#each why as w, i (i)}<li>{w}</li>{/each}
      </ul>
    </div>
  {/if}

  {#each plan.warnings as w, i (i)}
    <p class="banner warn">{w}</p>
  {/each}

  <section class="docs" aria-labelledby="docs-title">
    <h2 id="docs-title">Document changes</h2>
    {#if docs === undefined}
      <p class="muted" id="docs-missing">
        This server does not list the document changes of a plan. Update Plumb to see them before
        you approve.
      </p>
    {:else if docs.length === 0}
      <p class="muted" id="docs-none">
        {plan.status === "approved" ? "This plan changed" : "This plan changes"} no document.
      </p>
    {:else}
      <p id="docs-count">
        {plan.status === "approved" ? "The approval committed" : "Approving commits"}
        {docCount.added} added, {docCount.changed} changed, {docCount.removed} removed document{docs.length ===
        1
          ? ""
          : "s"}{plan.base_revision !== null ? ` (base: revision ${plan.base_revision})` : ""}.
        Secret values are never shown, only the names of the secrets.
      </p>
      {#if plan.documents_truncated}
        <p class="banner warn" id="docs-truncated" role="note">
          This plan changes too much text to show here. Some documents are listed by name only.
        </p>
      {/if}
      {#each shownDocs as c, i (`${c.kind}/${c.name}`)}
        {#if noText(c)}
          <p class="card doc-name" id="pdoc-{i}">
            <Badge tone={opTone[c.op]} label={opLabel[c.op]} />
            <span class="mono">{c.kind} {c.name}</span>
            <span class="muted">(too large to show here)</span>
          </p>
        {:else}
          <DocDiff change={c} id="pdoc-{i}" />
        {/if}
      {/each}
      {#if !allDocs && docs.length > FIRST_DOCS}
        <button class="btn" type="button" id="docs-all" onclick={() => (allDocs = true)}
          >Show the other {docs.length - FIRST_DOCS} documents</button
        >
      {/if}
    {/if}
  </section>

  {#each plan.pins ?? [] as pin, pi (pin.image)}
    <section class="card pin" aria-labelledby="pin-{pi}">
      <h2 id="pin-{pi}">Image <span class="mono">{pin.image}</span>: new base checksum</h2>
      <dl class="facts">
        <dt>Source</dt>
        <dd>{pinSource[pin.source]}</dd>
        <dt>Signature</dt>
        <dd>
          {#if pin.signed && pin.key_fingerprint}
            Signed by the key <span class="mono wrap">{pin.key_fingerprint}</span>
            (built into Plumb){#if pin.signed_at}, at {formatTime(pin.signed_at)}{/if}.
          {:else}
            <Badge tone="warn" label="not signed" /> The source has no signature. A person must check
            the new checksum.
          {/if}
        </dd>
        {#if pin.signed}
          <dt>Last accepted</dt>
          <dd>
            {pin.last_signed_at
              ? `A list signed at ${formatTime(pin.last_signed_at)}.`
              : "None: this is the first refresh of this image."}
          </dd>
        {/if}
        <dt>File</dt>
        <dd class="mono wrap">{pin.file}</dd>
        <dt>Old {pin.algorithm}</dt>
        <dd class="mono wrap" id="pin-old-{pi}">{pin.old ?? "–"}</dd>
        <dt>New {pin.algorithm}</dt>
        <dd class="mono wrap" id="pin-new-{pi}">{pin.new}</dd>
      </dl>
      {#if pin.used_before}
        <p class="warn" role="note">
          This image used the new checksum before: this is a step back to an older file.
        </p>
      {/if}
      <p class="muted">
        Only the pin changes. New versions and new Targets use the new file; existing Targets do not
        change.
      </p>
    </section>
  {/each}

  {#each plan.images ?? [] as part (part.image)}
    <ImagePlanPart
      {part}
      humanOnly={plan.human_only?.includes("image-recipe") ?? false}
      buildDiskGb={buildDisk[part.image]}
    />
  {/each}

  {#each plan.targets as t (t.target)}
    <h2>Target <span class="mono">{t.target}</span></h2>
    {#if t.secrets?.length}
      <p class="uses">
        Uses secrets:
        {#each t.secrets as sec, i (sec.name)}{i ? ", " : ""}<span class="mono">{sec.name}</span>
          <span class="muted">(version {sec.version})</span>{/each}. Values are never shown.
      </p>
    {/if}
    {#if t.resources.length === 0}
      <p class="muted">No resource changes on this target.</p>
    {/if}
    {#each t.resources as r, i (i)}
      <section
        class="card resource"
        class:restart={r.action === "restart"}
        aria-labelledby="res-{t.target}-{i}"
      >
        <div class="spread">
          <h3 id="res-{t.target}-{i}">
            <Badge
              tone={r.action === "delete" ? "danger" : r.action === "restart" ? "warn" : "info"}
              label={actionLabel[r.action]}
            />
            <span class="mono">{r.resource}</span>
          </h3>
          <RiskBadge risk={r.risk} />
        </div>
        {#if r.after?.length}
          <p class="order">
            after {#each r.after as a, j (j)}{j ? ", " : ""}<code class="mono">{a}</code>{/each}
          </p>
        {/if}
        {#if r.action === "restart" && r.restart_by?.length}
          <p class="order">
            restart because of {#each r.restart_by as c, j (j)}{j ? ", " : ""}<code class="mono"
                >{c}</code
              >{/each}
          </p>
        {:else if r.action === "restart"}
          <p class="muted">
            {r.resource.startsWith("guest:")
              ? "The guest restarts. Its services are down for a short time."
              : "The service restarts because a resource it depends on changes."}
          </p>
        {/if}
        {#if r.summary?.length}
          <ul class="summary" aria-label="Summary of {r.resource}">
            {#each r.summary as line, j (j)}<li>{line}</li>{/each}
          </ul>
        {/if}
        {#if r.confirm_seconds}
          <p class="muted safe">
            Safe apply: after the change the agent must reach Plumb again within {r.confirm_seconds} s.
            Else it puts the old state back by itself, and the step fails as "rolled back".
          </p>
        {/if}
        {#if r.changes.length === 0 && r.action === "create"}
          <p class="muted">
            Not observed yet: the guest does not exist. The agent observes and applies it at the
            first boot, within the approved risk.
          </p>
        {:else}
          {#if r.unverified}
            <p class="muted">
              Not observed before the approval: this resource gets a new secret (or a value that the
              import writes), so Plumb does not send the value before you approve. The agent
              observes it at the apply, within the approved risk.
            </p>
          {/if}
          <ChangeTable
            changes={r.changes}
            caption="Changes of {r.resource}"
            resource={r.resource}
            fromLabel="Now on the guest"
            toLabel="After the apply"
          />
        {/if}
      </section>
    {/each}
  {/each}

  {#if plan.status === "pending"}
    <form class="card" onsubmit={approve}>
      <h2>Approve</h2>
      {#if canApprove(plan)}
        <div class="field">
          <label for="approve-comment">Approval comment (optional)</label>
          <textarea id="approve-comment" bind:value={comment}></textarea>
        </div>
      {:else}
        <p class="muted" id="approve-role">
          Your role is {roleOf(me.session)}. {plan.risk === "destructive"
            ? "A destructive plan needs the role admin."
            : "Approving needs the role apply or admin."} Ask a person with that role.
        </p>
      {/if}
      <ProblemBox error={approveError} />
      <div class="row">
        {#if canApprove(plan)}
          <button
            class="btn primary"
            type="submit"
            disabled={busy || restored.mode}
            aria-describedby={restored.mode ? "approve-restored" : undefined}
            >Approve and apply</button
          >
        {/if}
        <a class="btn" href={href({ name: "plans" })}>Not now</a>
        {#if !confirmDrop && can("discardPlan")}
          <button class="btn danger" type="button" disabled={busy} onclick={askDrop}
            >Drop plan</button
          >
        {/if}
      </div>
      {#if confirmDrop}
        <div class="drop" role="group" aria-labelledby="drop-question">
          <p id="drop-question">
            Drop plan {id}? It can never be applied after that. An issue that it would resolve is
            open again.
          </p>
          <div class="row">
            <button
              id="drop-confirm"
              class="btn danger"
              type="button"
              disabled={busy}
              onclick={drop}>Drop</button
            >
            <button class="btn" type="button" onclick={() => (confirmDrop = false)}>Cancel</button>
          </div>
        </div>
      {/if}
      <ProblemBox error={dropError} />
      {#if restored.mode}<p class="muted" id="approve-restored">{RESTORED_WAIT}</p>{/if}
    </form>
  {:else}
    <ProblemBox error={approveError} />
  {/if}
{:else if !error}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .docs {
    margin-bottom: var(--space-4);
  }
  .doc-name {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }
  .pin h2 {
    margin-top: 0;
  }
  .wrap {
    overflow-wrap: anywhere;
  }
  .drop {
    margin-top: var(--space-3);
    border-left: 4px solid var(--color-danger);
    padding-left: var(--space-3);
  }
  .drop p {
    margin: 0 0 var(--space-2);
  }
  .why {
    margin: var(--space-1) 0 0;
    padding-left: var(--space-6);
  }
  .uses {
    margin: 0 0 var(--space-2);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-1) var(--space-4);
    margin: 0;
  }
  dt {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  dd {
    margin: 0;
  }
  h3 {
    font-size: var(--text-md);
    margin: 0 0 var(--space-2);
    display: flex;
    gap: var(--space-2);
    align-items: center;
    flex-wrap: wrap;
  }
  .summary {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .order {
    margin: 0 0 var(--space-2);
    color: var(--color-text-muted);
    overflow-wrap: anywhere;
  }
  .order code {
    color: var(--color-text);
  }
  .restart {
    border-left: 4px solid var(--color-warn);
  }
</style>
