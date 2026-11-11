<script lang="ts">
  import targetSchema from "../../../internal/state/schema/target.json";
  import type { PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import {
    fieldsOf,
    initialValues,
    mergeParams,
    validate,
    type Field,
    type Values,
  } from "../lib/schemaForm";
  import type { BlockView, ImageView, TargetSpec, TargetView } from "../lib/types";
  import {
    ALLOW_RESTART_HELP,
    ALLOW_RESTART_LABEL,
    CREATE_ONLY,
    REENROLL_REFUSED,
    WAITING_ACTIONS,
    formatTime,
    waitingLines,
    waitingSince,
  } from "../lib/view";
  import { smallestDisk } from "../lib/images";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import SchemaFields from "../ui/SchemaFields.svelte";

  let { api, name }: { api: PlumbApi; name?: string } = $props();

  // One schema source (D-043): the guest form comes from the Target schema.
  const specSchema = targetSchema.properties.spec;
  const guestFields: Field[] = fieldsOf(specSchema.properties.guest);
  const namePattern = new RegExp(targetSchema.properties.name.pattern, "u");
  const editing = $derived(name !== undefined);

  interface Use {
    key: number;
    use: string;
    id: string;
    fields: Field[];
    values: Values;
    /** The stored parameters; keys the form cannot show are kept on save. */
    stored: Record<string, unknown>;
    errors: Record<string, string>;
  }

  let images = $state<ImageView[]>([]);
  let blocks = $state<BlockView[]>([]);
  let existing = $state<TargetView | null>(null);
  let loaded = $state(false);
  let loadError = $state<unknown>(null);

  let targetName = $state("");
  let image = $state("");
  let guest = $state<Values>({});
  let uses = $state<Use[]>([]);
  let reason = $state("");
  let addChoice = $state("");
  let errors = $state<Record<string, string>>({});
  let guestErrors = $state<Record<string, string>>({});
  let submitError = $state<unknown>(null);
  let busy = $state(false);
  let allowRestart = $state(false);
  let reenrollReason = $state("");
  let reenrollError = $state<unknown>(null);
  let reenrollBusy = $state(false);
  let nextKey = 0;

  const waitingFrom = $derived(waitingSince(existing?.status.waiting ?? []));
  // The stored setting decides the re-enrollment, not the unsaved checkbox.
  const storedAllow = $derived(existing?.document.spec.allow_restart === true);

  // D-192: a stock Ubuntu 26.04 image takes a static address only from its
  // second boot. A short warning at the address field, only when it matters.
  const addressNote = $derived.by(() => {
    const ip = String(guest["ip"] ?? "dhcp");
    const img = images.find((i) => i.document.name === image);
    const label = `${image} ${img?.document.spec.title ?? ""}`;
    if (editing || ip === "dhcp" || !/ubuntu/i.test(label) || !/26\.04/.test(label)) return {};
    return {
      ip: "Ubuntu 26.04 takes a static address only from its second boot (D-192). Use DHCP.",
    } as Record<string, string>;
  });

  // D-202: a Target from a built image needs at least the disk of its template.
  const diskNote = $derived.by(() => {
    const min = smallestDisk(images.find((i) => i.document.name === image)?.document.spec);
    if (editing || min === null) return {} as Record<string, string>;
    return { disk: `At least ${min} GiB: the template of image ${image} has ${min} GiB.` };
  });

  const blockByName = (n: string) => blocks.find((b) => b.document.name === n);

  function makeUse(use: string, id = "", params: Record<string, unknown> = {}): Use {
    const schema = blockByName(use)?.document.spec.params;
    const fields = fieldsOf(schema);
    return {
      key: nextKey++,
      use,
      id,
      fields,
      values: initialValues(fields, params),
      stored: params,
      errors: {},
    };
  }

  $effect(() => {
    Promise.all([
      api.listImages(),
      api.listBlocks(),
      name ? api.getTarget(name) : Promise.resolve(null),
    ]).then(
      ([i, b, t]) => {
        images = i;
        blocks = b;
        existing = t;
        addChoice = b[0]?.document.name ?? "";
        if (t) {
          targetName = t.document.name;
          image = t.document.spec.image;
          guest = initialValues(guestFields, t.document.spec.guest);
          allowRestart = t.document.spec.allow_restart === true;
          uses = (t.document.spec.blocks ?? []).map((u) =>
            makeUse(u.use, u.id ?? "", u.params ?? {}),
          );
        } else {
          image = i[0]?.document.name ?? "";
          guest = initialValues(guestFields, {
            cores: 2,
            memory: 2048,
            disk: 20,
            bridge: "vmbr0",
            ip: "dhcp",
          });
        }
        loaded = true;
      },
      (e: unknown) => (loadError = e),
    );
  });

  function addBlock() {
    if (!addChoice) return;
    uses = [...uses, makeUse(addChoice)];
    const key = uses[uses.length - 1]?.key;
    queueMicrotask(() => document.getElementById(`use-${key}-legend`)?.focus());
  }

  function removeBlock(key: number) {
    uses = uses.filter((u) => u.key !== key);
    queueMicrotask(() => document.getElementById("add-block")?.focus());
  }

  function check(): boolean {
    const e: Record<string, string> = {};
    if (!editing && !namePattern.test(targetName)) {
      e["name"] =
        "Use lower-case letters, digits, '-' and '.'; start and end with a letter or digit.";
    }
    if (!image) e["image"] = "Choose an image.";
    if (!reason.trim()) e["reason"] = "Say why you make this change.";
    guestErrors = validate(guestFields, guest);
    // D-202: the disk of a Target from a built image cannot be smaller than the template.
    const min = smallestDisk(images.find((i) => i.document.name === image)?.document.spec);
    if (!editing && min !== null && !guestErrors["disk"] && Number(guest["disk"]) < min)
      guestErrors = {
        ...guestErrors,
        disk: `At least ${min} GiB: the template of image ${image} has ${min} GiB, and a disk cannot shrink.`,
      };
    const seen: string[] = [];
    for (const u of uses) {
      u.errors = validate(u.fields, u.values);
      const id = u.id.trim() || u.use;
      if (seen.includes(id)) u.errors["__id"] = `Two uses of a Block need different ids ("${id}").`;
      else if (u.id.trim() && !namePattern.test(u.id.trim()))
        u.errors["__id"] = "The id has the wrong format.";
      seen.push(id);
    }
    errors = e;
    return (
      Object.keys(e).length === 0 &&
      Object.keys(guestErrors).length === 0 &&
      uses.every((u) => Object.keys(u.errors).length === 0)
    );
  }

  async function submit(ev: SubmitEvent) {
    ev.preventDefault();
    submitError = null;
    if (!check()) {
      queueMicrotask(() =>
        (document.querySelector('[aria-invalid="true"]') as HTMLElement | null)?.focus(),
      );
      return;
    }
    const spec: TargetSpec = {
      type: "vm",
      image,
      // On an existing target the create-only fields keep their stored
      // value: a schema default the form shows must not become a change.
      guest: mergeParams(
        existing?.document.spec.guest ?? {},
        editing
          ? guestFields.filter((f) => !(CREATE_ONLY as readonly string[]).includes(f.name))
          : guestFields,
        guest,
      ) as TargetSpec["guest"],
      blocks: uses.map((u) => ({
        use: u.use,
        ...(u.id.trim() ? { id: u.id.trim() } : {}),
        params: mergeParams(u.stored, u.fields, u.values),
      })),
      overrides: existing?.document.spec.overrides ?? {},
      // Written only when set or stored before, so an old Target gets no
      // new key by an unrelated edit (default false, D-194).
      ...(allowRestart || existing?.document.spec.allow_restart !== undefined
        ? { allow_restart: allowRestart }
        : {}),
    };
    busy = true;
    try {
      const plan = await api.createPlan({
        reason,
        put: [{ kind: "Target", name: targetName, spec }],
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      submitError = err;
    } finally {
      busy = false;
    }
  }

  async function reenroll(ev: SubmitEvent) {
    ev.preventDefault();
    if (!name) return;
    reenrollError = null;
    reenrollBusy = true;
    try {
      const plan = await api.reenrollTarget(name, reenrollReason.trim());
      go({ name: "plan", id: plan.id });
    } catch (err) {
      reenrollError = err;
    } finally {
      reenrollBusy = false;
    }
  }
</script>

<h1 id="page-title" tabindex="-1">{editing ? `Edit target ${name}` : "New target"}</h1>
<ProblemBox error={loadError} />
{#if existing?.status.ignored_drift?.length}
  {@const drift = existing.status.ignored_drift}
  <section class="card" aria-labelledby="ignored-title">
    <h2 id="ignored-title">Ignored drift</h2>
    <p>
      {drift.length} difference{drift.length === 1 ? "" : "s"} on this guest
      {drift.length === 1 ? "is" : "are"} ignored by a rule. The next apply still writes the desired state.
    </p>
    <ul class="drift">
      {#each drift as d (d.resource)}
        <li>
          <span class="mono">{d.resource}</span>: {d.attrs.join(", ") || "all attributes"} · rule
          <span class="mono">{d.rule}</span> · last seen {formatTime(d.last_seen)}
        </li>
      {/each}
    </ul>
    <a href={href({ name: "rules" })}>See the rules</a>
  </section>
{/if}
{#if existing && waitingFrom}
  <section class="card waiting" aria-labelledby="waiting-title">
    <h2 id="waiting-title">Restart needed</h2>
    <p>Restart needed since {formatTime(waitingFrom)}.</p>
    <ul>
      {#each waitingLines(existing.status.waiting) as line (line)}
        <li>{line}</li>
      {/each}
    </ul>
    <p>{WAITING_ACTIONS}</p>
  </section>
{/if}
{#if loaded}
  <form onsubmit={submit} novalidate>
    <fieldset>
      <legend>Target</legend>
      <div class="grid">
        <div class="field">
          <label for="t-name"
            >Name{#if !editing}<span aria-hidden="true"> *</span>{/if}</label
          >
          <input
            id="t-name"
            type="text"
            bind:value={targetName}
            readonly={editing}
            required
            aria-invalid={!!errors["name"]}
            aria-describedby={errors["name"] ? "t-name-error" : editing ? "t-name-hint" : undefined}
          />
          {#if editing}<span class="hint" id="t-name-hint">The name cannot change.</span>{/if}
          {#if errors["name"]}<span class="error" id="t-name-error">{errors["name"]}</span>{/if}
        </div>
        <div class="field">
          <label for="t-image">Image</label>
          <select
            id="t-image"
            bind:value={image}
            disabled={editing}
            aria-describedby={editing ? "t-image-hint" : undefined}
          >
            {#each images as i (i.document.name)}
              <option value={i.document.name}>{i.document.spec.title ?? i.document.name}</option>
            {/each}
          </select>
          {#if editing}<span class="hint" id="t-image-hint"
              >{#if existing?.status.image_version}<span id="t-image-version" class="block"
                  >Made from image {existing.document.spec.image} v{existing.status
                    .image_version}.</span
                >
              {/if}The image is used only to create the guest.</span
            >{/if}
        </div>
        <div class="field">
          <span class="label">Type</span>
          <span>Virtual machine</span>
        </div>
      </div>
    </fieldset>

    <fieldset>
      <legend>Guest settings</legend>
      <SchemaFields
        fields={guestFields}
        bind:values={guest}
        errors={guestErrors}
        prefix="guest"
        readonly={editing ? CREATE_ONLY : []}
        readonlyHint="Create-only: it cannot change on an existing target."
        notes={{ ...addressNote, ...diskNote }}
      />
      <div class="field">
        <label class="check">
          <input
            type="checkbox"
            id="t-allow-restart"
            bind:checked={allowRestart}
            aria-describedby="t-allow-restart-hint"
          />
          {ALLOW_RESTART_LABEL}
        </label>
        <span class="hint" id="t-allow-restart-hint">{ALLOW_RESTART_HELP}</span>
      </div>
    </fieldset>

    <h2>Blocks</h2>
    {#each uses as u (u.key)}
      {@const b = blockByName(u.use)}
      <fieldset aria-describedby={b?.document.spec.description ? `use-${u.key}-desc` : undefined}>
        <legend id="use-{u.key}-legend" tabindex="-1">{b?.document.spec.title ?? u.use}</legend>
        {#if b?.document.spec.description}<p class="muted" id="use-{u.key}-desc">
            {b.document.spec.description}
          </p>{/if}
        <div class="field">
          <label for="use-{u.key}-id">Use id (optional)</label>
          <input
            id="use-{u.key}-id"
            type="text"
            bind:value={u.id}
            placeholder={u.use}
            aria-invalid={!!u.errors["__id"]}
            aria-describedby="use-{u.key}-id-hint{u.errors['__id'] ? ` use-${u.key}-id-error` : ''}"
          />
          <span class="hint" id="use-{u.key}-id-hint"
            >Needed when the same Block is used twice.</span
          >
          {#if u.errors["__id"]}<span class="error" id="use-{u.key}-id-error"
              >{u.errors["__id"]}</span
            >{/if}
        </div>
        {#if u.fields.length}
          <SchemaFields
            fields={u.fields}
            bind:values={u.values}
            errors={u.errors}
            prefix="use-{u.key}"
            {api}
          />
        {:else}
          <p class="muted">This Block has no parameters.</p>
        {/if}
        <button type="button" class="btn danger" onclick={() => removeBlock(u.key)}>
          Remove {b?.document.spec.title ?? u.use}
        </button>
      </fieldset>
    {:else}
      <p class="muted">No Blocks yet.</p>
    {/each}
    <div class="row add">
      <label for="add-choice" class="sr-only">Block to add</label>
      <select id="add-choice" bind:value={addChoice}>
        {#each blocks as b (b.document.name)}
          <option value={b.document.name}>{b.document.spec.title ?? b.document.name}</option>
        {/each}
      </select>
      <button type="button" class="btn" id="add-block" onclick={addBlock}>Add Block</button>
    </div>

    <h2>Review</h2>
    <div class="field">
      <label for="t-reason">Reason<span aria-hidden="true"> *</span></label>
      <textarea
        id="t-reason"
        bind:value={reason}
        required
        aria-invalid={!!errors["reason"]}
        aria-describedby="t-reason-hint{errors['reason'] ? ' t-reason-error' : ''}"></textarea>
      <span class="hint" id="t-reason-hint">Stored with the revision (provenance).</span>
      {#if errors["reason"]}<span class="error" id="t-reason-error">{errors["reason"]}</span>{/if}
    </div>
    <ProblemBox error={submitError} />
    <div class="row">
      <button class="btn primary" type="submit" disabled={busy}>Show the plan</button>
      <a class="btn" href={href({ name: "targets" })}>Cancel</a>
    </div>
    <p class="muted">Nothing changes until you approve the plan.</p>
  </form>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}
{#if loaded && existing}
  <form class="card" onsubmit={reenroll} aria-labelledby="reenroll-title">
    <h2 id="reenroll-title">Re-enroll the agent</h2>
    <p>
      A new enrollment token becomes active at the next stop and start of the VM. Plumb plans it;
      nothing happens until you approve the plan.
    </p>
    {#if storedAllow}
      <p>
        The Run restarts the VM (or starts it when it is stopped). '{ALLOW_RESTART_LABEL}' stays on
        after it, until you switch it off.
      </p>
    {:else if existing.status.power === "running"}
      <p class="warn" id="reenroll-refused">
        {REENROLL_REFUSED} (The VM ran when this page was loaded; the server checks again.)
      </p>
    {:else}
      <p>The VM is stopped: the Run starts it with the new token.</p>
    {/if}
    <div class="field">
      <label for="reenroll-reason">Reason (optional)</label>
      <textarea id="reenroll-reason" bind:value={reenrollReason} maxlength="2000"></textarea>
    </div>
    <ProblemBox error={reenrollError} />
    <button class="btn" type="submit" disabled={reenrollBusy}>Plan the re-enrollment</button>
  </form>
{/if}

<style>
  .waiting {
    border-left: 4px solid var(--color-warn);
  }
  .waiting ul {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .warn {
    color: var(--color-warn);
  }
  .block {
    display: block;
  }
  .drift {
    margin: 0 0 var(--space-2);
    padding-left: var(--space-6);
  }
  .add {
    margin-bottom: var(--space-4);
  }
  legend:focus {
    outline: 2px solid var(--color-accent);
  }
</style>
