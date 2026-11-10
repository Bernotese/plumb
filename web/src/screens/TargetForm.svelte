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
  import { CREATE_ONLY } from "../lib/view";
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
  let nextKey = 0;

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
      guest: mergeParams(
        existing?.document.spec.guest ?? {},
        guestFields,
        guest,
      ) as TargetSpec["guest"],
      blocks: uses.map((u) => ({
        use: u.use,
        ...(u.id.trim() ? { id: u.id.trim() } : {}),
        params: mergeParams(u.stored, u.fields, u.values),
      })),
      overrides: existing?.document.spec.overrides ?? {},
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
</script>

<h1 id="page-title" tabindex="-1">{editing ? `Edit target ${name}` : "New target"}</h1>
<ProblemBox error={loadError} />
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
              >The image is used only to create the guest.</span
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
      />
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

<style>
  .add {
    margin-bottom: var(--space-4);
  }
  legend:focus {
    outline: 2px solid var(--color-accent);
  }
</style>
