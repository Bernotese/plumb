<script lang="ts">
  // Adoption (T-194; D-373, D-378, D-386, ARCHITECTURE §19): guests that a
  // person put into the pool `plumb` and that no Target holds. Adopting
  // one is a human-only Plan that puts a new Target bound to the guest; the
  // Run writes nothing to Proxmox. The agent comes later, by an install
  // command on the Target's agent page.
  import { tick } from "svelte";
  import { ApiError } from "../lib/api";
  import type { PlumbApi, UnmanagedGuest } from "../lib/client";
  import { describedBy, pointerErrors } from "../lib/formErrors";
  import { go, href } from "../lib/router";
  import { can, roleNote } from "../lib/session.svelte";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();

  let guests = $state<UnmanagedGuest[] | null>(null);
  let loadError = $state<unknown>(null);
  let chosen = $state<UnmanagedGuest | null>(null);
  let name = $state("");
  let reason = $state("");
  let errors = $state<Record<string, string>>({});
  let submitError = $state<unknown>(null);
  let busy = $state(false);

  const NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;
  const REASON: Record<string, string> = {
    "disk-layout":
      "the disks do not fit a Target (a VM needs exactly one disk scsi0; a container no mount point)",
    "disk-storage": "a disk is on another storage than the Target disk storage of the install",
    "nic-layout": "it needs exactly one network card net0",
    bridge: "its bridge has no ACL line of the install",
    vlan: "its network card has a VLAN tag",
    privileged: "it is a privileged container",
    "host-device": "it uses a host device (devN, usbN or hostpciN)",
    template: "it is a template",
    name: "its name is not a valid document name; rename it in Proxmox",
  };
  const WARNING: Record<string, string> = {
    "not-sealed":
      "Not sealed: its SSH host keys, machine-id and root users stay as they are (Plumb did not make it).",
    nesting: "Nesting is on.",
    keyctl: "keyctl is on.",
    "no-cloud-init":
      "No cloud-init: the create-only fields stay as Proxmox has them; Plumb does not compare them.",
  };
  const PRESENT = {
    cipassword: "cipassword",
    sshkeys: "sshkeys",
    description: "description",
    env: "env",
  };
  const reasonText = (r: string) => REASON[r] ?? r;

  async function load() {
    loadError = null;
    try {
      guests = await api.listUnmanagedGuests();
    } catch (e) {
      loadError = e;
    }
  }
  $effect(() => {
    void load();
  });

  async function choose(g: UnmanagedGuest) {
    chosen = g;
    name = NAME.test(g.name) ? g.name : "";
    reason = "";
    errors = {};
    submitError = null;
    await tick();
    document.getElementById("adopt-title")?.focus();
  }

  const fields = (g: UnmanagedGuest) => Object.entries(g.guest as Record<string, unknown>);
  const present = (g: UnmanagedGuest) =>
    (Object.keys(PRESENT) as (keyof typeof PRESENT)[]).filter((k) => g.present[k]);
  const size = (g: UnmanagedGuest) => {
    const x = g.guest as { cores?: number; memory?: number; disk?: number };
    return [
      x.cores !== undefined ? `${x.cores} ${x.cores === 1 ? "core" : "cores"}` : "",
      x.memory !== undefined ? `${x.memory} MiB` : "",
      x.disk !== undefined ? `${x.disk} GiB` : "",
    ]
      .filter(Boolean)
      .join(" · ");
  };

  async function submit(ev: SubmitEvent) {
    ev.preventDefault();
    if (!chosen) return;
    const e: Record<string, string> = {};
    if (!NAME.test(name))
      e["name"] =
        "Use lower-case letters, digits, '-' and '.'; start and end with a letter or digit.";
    errors = e;
    if (Object.keys(e).length) {
      await tick();
      document.getElementById("adopt-name")?.focus();
      return;
    }
    busy = true;
    submitError = null;
    try {
      const plan = await api.adoptTarget(name, {
        node: chosen.node,
        vmid: chosen.vmid,
        digest: chosen.digest,
        ...(reason.trim() ? { reason: reason.trim() } : {}),
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      submitError = err;
      const p = pointerErrors(err);
      if (p["/name"]) errors = { name: p["/name"] };
      // The guest changed or is gone: the list is old.
      if (err instanceof ApiError && err.problem.status === 409) void load();
    } finally {
      busy = false;
    }
  }
</script>

<p class="back"><a href={href({ name: "targets" })}>← Targets</a></p>
<h1 id="page-title" tabindex="-1">Adopt a guest</h1>
<p class="muted">
  Guests that a person put into the Proxmox pool <span class="mono">plumb</span> and that no Target holds.
  Plumb never moves a guest into the pool. Adopting makes a new Target for the guest by a plan; Plumb
  changes nothing in Proxmox for it.
</p>
<ProblemBox error={loadError} />

{#if guests && guests.length === 0}
  <p class="banner info" id="adopt-empty">
    No guest of the pool is free. Move a guest into the pool <span class="mono">plumb</span> in Proxmox
    to adopt it.
  </p>
{:else if guests}
  <div class="card flush">
    <table>
      <caption class="sr-only">Guests of the pool without a Target</caption>
      <thead>
        <tr>
          <th scope="col">Guest</th>
          <th scope="col">Type</th>
          <th scope="col">Size</th>
          <th scope="col">State</th>
          <th scope="col"><span class="sr-only">Action</span></th>
        </tr>
      </thead>
      <tbody>
        {#each guests as g (`${g.node}/${g.vmid}`)}
          <tr>
            <td data-label="Guest">
              <span class="mono">{g.name}</span>
              <span class="sub mono">{g.node} · {g.vmid}{g.power ? ` · ${g.power}` : ""}</span>
            </td>
            <td data-label="Type">{g.type === "lxc" ? "Container" : "VM"}</td>
            <td data-label="Size">{size(g)}</td>
            <td data-label="State" id="guest-{g.vmid}-state">
              {#if g.adoptable}
                <Badge tone="ok" label="adoptable" />
              {:else}
                <Badge tone="muted" label="not adoptable" />
                <ul class="reasons">
                  {#each g.reasons as r (r)}<li>{reasonText(r)}</li>{/each}
                </ul>
              {/if}
            </td>
            <td data-label="Action">
              {#if g.adoptable && can("adoptTarget")}
                <button
                  class="btn"
                  type="button"
                  aria-label="Adopt {g.name} ({g.vmid})"
                  onclick={() => choose(g)}>Adopt…</button
                >
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if !can("adoptTarget")}<p class="muted" id="adopt-role">{roleNote("plan")}</p>{/if}
  <p><button class="btn" type="button" onclick={load}>Reload the list</button></p>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

{#if chosen}
  <form class="card" onsubmit={submit} novalidate aria-labelledby="adopt-title">
    <h2 id="adopt-title" tabindex="-1">
      Adopt <span class="mono">{chosen.name}</span> ({chosen.type === "lxc" ? "container" : "VM"}
      {chosen.vmid})
    </h2>
    <ul class="warnings" id="adopt-warnings">
      {#each chosen.warnings as w (w)}<li>{WARNING[w] ?? w}</li>{/each}
    </ul>
    <p>The new Target holds what Proxmox has now:</p>
    <dl class="facts" id="adopt-fields">
      <dt>Type</dt>
      <dd>{chosen.type}</dd>
      {#each fields(chosen) as [k, v] (k)}
        <dt>{k}</dt>
        <dd class="mono">{String(v)}</dd>
      {/each}
    </dl>
    <p class="muted" id="adopt-present">
      {#if present(chosen).length}The Proxmox config holds {present(chosen).join(", ")}. Plumb never
        reads {present(chosen).length === 1 ? "its value" : "their values"} and does not take {present(
          chosen,
        ).length === 1
          ? "it"
          : "them"} into the Target.{:else}Plumb never takes passwords, SSH keys, the description or
        an environment of the guest into the Target.{/if}
      The Target gets no Image. Deleting it later keeps the guest unless you choose otherwise.
    </p>
    <div class="field">
      <label for="adopt-name">Name of the new Target<span aria-hidden="true"> *</span></label>
      <input
        id="adopt-name"
        type="text"
        autocomplete="off"
        bind:value={name}
        aria-invalid={!!errors["name"]}
        aria-describedby={describedBy("adopt-name", errors["name"])}
      />
      <span class="hint" id="adopt-name-hint">The Proxmox name stays as it is.</span>
      {#if errors["name"]}<span class="error" id="adopt-name-error">{errors["name"]}</span>{/if}
    </div>
    <div class="field">
      <label for="adopt-reason">Reason (optional)</label>
      <textarea id="adopt-reason" bind:value={reason}></textarea>
    </div>
    <ProblemBox error={submitError} fields={["/name"]} />
    <div class="row">
      <button class="btn primary" type="submit" disabled={busy}>Show the plan</button>
      <button class="btn" type="button" onclick={() => (chosen = null)}>Cancel</button>
    </div>
    <p class="muted">
      Only a person can approve the plan. After it, install the agent with the install command on
      the Target's agent page.
    </p>
  </form>
{/if}

<style>
  .flush {
    padding: 0;
    overflow: hidden;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: left;
    padding: var(--space-2) var(--space-3);
    border-bottom: 1px solid var(--color-border);
    vertical-align: top;
  }
  .sub {
    display: block;
    font-size: var(--text-xs);
    color: var(--color-text-muted);
  }
  .reasons {
    margin: var(--space-1) 0 0;
    padding-left: var(--space-4);
    font-size: var(--text-sm);
    color: var(--color-text-muted);
  }
  .warnings {
    margin: 0 0 var(--space-3);
    padding-left: var(--space-6);
    color: var(--color-danger);
  }
  .facts {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--space-1) var(--space-4);
    margin: 0 0 var(--space-3);
  }
  dt {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  dd {
    margin: 0;
  }
</style>
