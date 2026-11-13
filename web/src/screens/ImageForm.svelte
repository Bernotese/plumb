<script lang="ts">
  // Only "properties": the "examples" of the schema hold demo data that must not ship (check-dist).
  import { properties as imageProps } from "../../../internal/state/schema/image.json";
  import type { PlumbApi } from "../lib/client";
  import { pointerErrors } from "../lib/formErrors";
  import {
    KNOWN_LXC_TEMPLATES,
    WEEKDAYS,
    nextSlot,
    weekdayName,
    type Schedule,
    type Weekday,
  } from "../lib/images";
  import { go, href } from "../lib/router";
  import {
    fieldsOf,
    initialValues,
    mergeParams,
    validate,
    type Field,
    type Values,
  } from "../lib/schemaForm";
  import { unfitText } from "../lib/blocks";
  import type { BlockView, ImageSpec, ImageView } from "../lib/types";
  import { KIND_NAME, type GuestKind } from "../lib/view";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import SchemaFields from "../ui/SchemaFields.svelte";

  let { api, name }: { api: PlumbApi; name?: string } = $props();

  // One schema source (D-043): the build guest form comes from the Image schema.
  const specProps = imageProps.spec.properties;
  const guestFields: Field[] = fieldsOf(specProps.build.properties.guest as never);
  const namePattern = new RegExp(imageProps.name.pattern, "u");
  // A VM Image needs https://; an lxc Image may use http://, its pin is the trust (D-224).
  const urlPattern: Record<GuestKind, RegExp> = { vm: /^https:\/\//u, lxc: /^https?:\/\//u };
  const shaPattern: Record<GuestKind, RegExp> = {
    vm: new RegExp(specProps.sha256.pattern, "u"),
    lxc: new RegExp(specProps.sha512.pattern, "u"),
  };
  const atPattern = new RegExp(specProps.schedule.properties.at.pattern, "u");
  // The root file system of a container needs at least 2 GiB (T-075, minLXCDisk).
  const MIN_LXC_DISK = 2;
  const editing = $derived(name !== undefined);

  interface Use {
    key: number;
    use: string;
    id: string;
    fields: Field[];
    values: Values;
    stored: Record<string, unknown>;
    errors: Record<string, string>;
  }

  let existing = $state<ImageView | null>(null);
  let keyedTargets = $state<string[]>([]);
  let allBlocks = $state<BlockView[]>([]);
  let loaded = $state(false);
  let loadError = $state<unknown>(null);

  let imageName = $state("");
  let kind = $state<GuestKind>("vm");
  let known = $state("");
  let title = $state("");
  let url = $state("");
  let sha = $state("");
  let build = $state(false);
  let upgrade = $state(true);
  let guest = $state<Values>({});
  let uses = $state<Use[]>([]);
  let addChoice = $state("");
  let keep = $state(3);
  let every = $state<"none" | Schedule["every"]>("none");
  let weekday = $state<Weekday>("sun");
  let day = $state(1);
  let at = $state("03:00");
  let reason = $state("");
  let errors = $state<Record<string, string>>({});
  let guestErrors = $state<Record<string, string>>({});
  let submitError = $state<unknown>(null);
  let busy = $state(false);
  let nextKey = 0;

  /** A Block with a secret parameter cannot go into an Image (D-202). */
  const hasSecret = (b: BlockView) =>
    Object.values(b.document.spec.params?.properties ?? {}).some(
      (p) => (p as { writeOnly?: boolean }).writeOnly === true,
    );
  // T-118 (D-260): also only Blocks that fit the guest type.
  const usable = $derived(
    allBlocks.filter((b) => !hasSecret(b) && unfitText(b.document, kind) === ""),
  );
  const secretBlocks = $derived(allBlocks.filter(hasSecret).map((b) => b.document.name));
  const unfitBlocks = $derived(
    allBlocks
      .filter((b) => !hasSecret(b) && unfitText(b.document, kind) !== "")
      .map((b) => b.document.name),
  );
  $effect(() => {
    if (!usable.some((b) => b.document.name === addChoice))
      addChoice = usable[0]?.document.name ?? "";
  });
  const unfitUse = (use: string) => {
    const b = blockByName(use);
    return b ? unfitText(b.document, kind) : "";
  };
  const blockByName = (n: string) => allBlocks.find((b) => b.document.name === n);
  const wasBuilt = $derived(existing?.document.spec.build !== undefined);

  const schedule = $derived<Schedule | undefined>(
    every === "none"
      ? undefined
      : every === "week"
        ? { every, weekday, at }
        : every === "month"
          ? { every, day, at }
          : { every, at },
  );
  const nextBuild = $derived.by(() => {
    if (!schedule || !atPattern.test(at) || (every === "month" && !(day >= 1 && day <= 28)))
      return null;
    return nextSlot(schedule, new Date());
  });

  function makeUse(use: string, id = "", params: Record<string, unknown> = {}): Use {
    const fields = fieldsOf(blockByName(use)?.document.spec.params);
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
      api.listBlocks(),
      name ? api.getImage(name) : Promise.resolve(null),
      name ? api.listTargets() : Promise.resolve([]),
    ]).then(
      ([b, i, t]) => {
        allBlocks = b;
        existing = i;
        // D-251: a Target of this Image with SSH keys blocks a build of it.
        keyedTargets = t
          .filter((x) => x.document.spec.image === name && x.document.spec.guest.ssh_keys?.length)
          .map((x) => x.document.name);
        const s = i?.document.spec;
        const g = s?.build?.guest;
        guest = initialValues(guestFields, {
          cores: 1,
          memory: 1024,
          disk: 10,
          bridge: "vmbr0",
          ip: "dhcp",
          ...g,
        });
        if (i && s) {
          imageName = i.document.name;
          title = s.title ?? "";
          kind = s.type;
          url = s.url;
          sha = (s.type === "lxc" ? s.sha512 : s.sha256) ?? "";
          build = s.build !== undefined;
          upgrade = s.build?.upgrade ?? true;
          uses = (s.build?.blocks ?? []).map((u) => makeUse(u.use, u.id ?? "", u.params ?? {}));
          keep = s.retention?.keep ?? 3;
          every = s.schedule?.every ?? "none";
          weekday = s.schedule?.weekday ?? "sun";
          day = s.schedule?.day ?? 1;
          at = s.schedule?.at ?? "03:00";
        }
        loaded = true;
      },
      (e: unknown) => (loadError = e),
    );
  });

  // Server field errors (422 pointers) next to their fields.
  const serverErrors = $derived(pointerErrors(submitError));
  const fieldError = (key: string, pointer: string) =>
    errors[key] ?? serverErrors[`/put/0/spec${pointer}`];

  function addBlock() {
    if (!addChoice) return;
    uses = [...uses, makeUse(addChoice)];
    const key = uses[uses.length - 1]?.key;
    queueMicrotask(() => document.getElementById(`iuse-${key}-legend`)?.focus());
  }

  function removeBlock(key: number) {
    uses = uses.filter((u) => u.key !== key);
    queueMicrotask(() => document.getElementById("add-block")?.focus());
  }

  function check(): boolean {
    const e: Record<string, string> = {};
    if (!editing && !namePattern.test(imageName))
      e["name"] =
        "Use lower-case letters, digits, '-' and '.'; start and end with a letter or digit.";
    if (!urlPattern[kind].test(url.trim()))
      e["url"] =
        kind === "lxc"
          ? "Give the http:// or https:// address of the Proxmox template."
          : "Give an https:// address of the cloud image.";
    if (!shaPattern[kind].test(sha.trim()))
      e[shaKey] =
        kind === "lxc"
          ? "Give the SHA-512 checksum: 128 characters 0-9 and a-f."
          : "Give the SHA-256 checksum: 64 characters 0-9 and a-f.";
    if (!reason.trim()) e["reason"] = "Say why you make this change.";
    guestErrors = build ? validate(kindGuestFields, guest) : {};
    if (build && kind === "lxc" && !guestErrors["disk"] && Number(guest["disk"]) < MIN_LXC_DISK)
      guestErrors = {
        ...guestErrors,
        disk: `The root file system of a container build needs at least ${MIN_LXC_DISK} GiB.`,
      };
    if (build) {
      if (!(Number.isInteger(keep) && keep >= 1 && keep <= 20))
        e["keep"] = "Keep 1 to 20 versions.";
      if (every !== "none" && !atPattern.test(at)) e["at"] = "Give a time as HH:MM (UTC).";
      if (every === "month" && !(Number.isInteger(day) && day >= 1 && day <= 28))
        e["day"] = "Choose a day from 1 to 28.";
      const seen: string[] = [];
      for (const u of uses) {
        u.errors = validate(u.fields, u.values);
        const id = u.id.trim() || u.use;
        if (seen.includes(id))
          u.errors["__id"] = `Two uses of a Block need different ids ("${id}").`;
        else if (u.id.trim() && !namePattern.test(u.id.trim()))
          u.errors["__id"] = "The id has the wrong format.";
        seen.push(id);
        const unfit = unfitUse(u.use);
        if (unfit) u.errors["__kind"] = unfit;
      }
    }
    errors = e;
    return (
      Object.keys(e).length === 0 &&
      Object.keys(guestErrors).length === 0 &&
      (!build || uses.every((u) => Object.keys(u.errors).length === 0))
    );
  }

  async function submit(ev: SubmitEvent) {
    ev.preventDefault();
    submitError = null;
    if (!check()) {
      queueMicrotask(() =>
        (
          document.querySelector('[aria-invalid="true"], .use-unfit') as HTMLElement | null
        )?.focus(),
      );
      return;
    }
    const old = existing?.document.spec;
    const spec: ImageSpec = {
      ...(title.trim() ? { title: title.trim() } : {}),
      type: kind,
      url: url.trim(),
      ...(kind === "lxc" ? { sha512: sha.trim() } : { sha256: sha.trim() }),
    };
    if (build) {
      spec.build = {
        blocks: uses.map((u) => ({
          use: u.use,
          ...(u.id.trim() ? { id: u.id.trim() } : {}),
          params: mergeParams(u.stored, u.fields, u.values),
        })),
        upgrade,
        guest: mergeParams(old?.build?.guest ?? {}, kindGuestFields, guest) as NonNullable<
          ImageSpec["build"]
        >["guest"],
      };
      spec.retention = { keep };
      if (schedule) spec.schedule = schedule;
    }
    busy = true;
    try {
      const plan = await api.createPlan({
        reason,
        put: [{ kind: "Image", name: imageName, spec }],
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      submitError = err;
    } finally {
      busy = false;
    }
  }

  const shaKey = $derived(kind === "lxc" ? "sha512" : "sha256");
  // The schema texts speak of VMs; a container build field gets its own help.
  const kindGuestFields = $derived(
    kind === "lxc"
      ? guestFields.map((f) =>
          f.name === "disk"
            ? {
                ...f,
                description: `GiB: the root file system of the build container, at least ${MIN_LXC_DISK}. A Target from this image needs at least this size.`,
              }
            : f.name === "ip"
              ? {
                  ...f,
                  description: "dhcp or an IPv4 address in CIDR form for the build container.",
                }
              : f,
        )
      : guestFields,
  );

  /** The pick list fills url and pin of a known Proxmox template. */
  function pickKnown(label: string) {
    known = label;
    const t = KNOWN_LXC_TEMPLATES.find((x) => x.label === label);
    if (!t) return;
    url = t.url;
    sha = t.sha512;
  }

  const desc = (id: string, err: string | undefined, hint = true) =>
    [err ? `${id}-error` : "", hint ? `${id}-hint` : ""].filter(Boolean).join(" ") || undefined;
</script>

<h1 id="page-title" tabindex="-1">{editing ? `Edit image ${name}` : "New image"}</h1>
<ProblemBox error={loadError} />
{#if loaded}
  <form onsubmit={submit} novalidate>
    <fieldset>
      <legend>Base image</legend>
      <div class="grid">
        <div class="field">
          <label for="i-name"
            >Name{#if !editing}<span aria-hidden="true"> *</span>{/if}</label
          >
          <input
            id="i-name"
            type="text"
            bind:value={imageName}
            readonly={editing}
            required
            aria-invalid={!!errors["name"]}
            aria-describedby={desc("i-name", errors["name"], editing)}
          />
          {#if editing}<span class="hint" id="i-name-hint">The name cannot change.</span>{/if}
          {#if errors["name"]}<span class="error" id="i-name-error">{errors["name"]}</span>{/if}
        </div>
        <div class="field">
          <label for="i-title">Title (optional)</label>
          <input id="i-title" type="text" bind:value={title} maxlength="100" />
        </div>
        {#if editing}
          <div class="field">
            <span class="label">Type</span>
            <span id="i-type">{KIND_NAME[kind]}</span>
          </div>
        {/if}
      </div>
      {#if !editing}
        <fieldset class="field plain-set" aria-describedby="i-type-hint">
          <legend class="label">Type</legend>
          <label class="check"
            ><input type="radio" name="i-type" value="vm" bind:group={kind} />
            {KIND_NAME.vm}</label
          >
          <label class="check"
            ><input type="radio" name="i-type" value="lxc" bind:group={kind} />
            {KIND_NAME.lxc}</label
          >
          <span class="hint" id="i-type-hint"
            >A Target uses an Image of its own type. The type cannot change later.</span
          >
        </fieldset>
      {/if}
      {#if kind === "lxc"}
        <div class="field">
          <label for="i-known">Known template</label>
          <select
            id="i-known"
            bind:value={known}
            onchange={(e) => pickKnown(e.currentTarget.value)}
            aria-describedby="i-known-hint"
          >
            <option value="">Choose to fill address and checksum</option>
            {#each KNOWN_LXC_TEMPLATES as t (t.label)}
              <option value={t.label}>{t.label}</option>
            {/each}
          </select>
          <span class="hint" id="i-known-hint"
            >The checksums come from the signed Proxmox appliance index.</span
          >
        </div>
      {/if}
      <div class="field">
        <label for="i-url">Download address<span aria-hidden="true"> *</span></label>
        <input
          id="i-url"
          oninput={() => (known = "")}
          type="text"
          inputmode="url"
          spellcheck="false"
          bind:value={url}
          required
          placeholder={kind === "lxc"
            ? "http://download.proxmox.com/images/system/…tar.zst"
            : "https://cloud.debian.org/images/cloud/…qcow2"}
          aria-invalid={!!fieldError("url", "/url")}
          aria-describedby={desc("i-url", fieldError("url", "/url"))}
        />
        <span class="hint" id="i-url-hint"
          >{kind === "lxc"
            ? "A Proxmox system template. Proxmox serves them over http only: the checksum is the trust, and the controller downloads the file itself."
            : "The official cloud image (qcow2 or img)."}</span
        >
        {#if fieldError("url", "/url")}<span class="error" id="i-url-error"
            >{fieldError("url", "/url")}</span
          >{/if}
      </div>
      <div class="field">
        <label for="i-sha"
          >{kind === "lxc" ? "SHA-512" : "SHA-256"} checksum<span aria-hidden="true">
            *</span
          ></label
        >
        <input
          id="i-sha"
          oninput={() => (known = "")}
          type="text"
          class="mono"
          bind:value={sha}
          required
          spellcheck="false"
          aria-invalid={!!fieldError(shaKey, `/${shaKey}`)}
          aria-describedby={desc("i-sha", fieldError(shaKey, `/${shaKey}`))}
        />
        <span class="hint" id="i-sha-hint"
          >{kind === "lxc"
            ? "Copy it from the sha512sum field of the signed Proxmox appliance index, never from the downloaded file. Plumb refuses any other file. Only a person can approve a new address or checksum."
            : "Plumb refuses any other file."}</span
        >
        {#if fieldError(shaKey, `/${shaKey}`)}<span class="error" id="i-sha-error"
            >{fieldError(shaKey, `/${shaKey}`)}</span
          >{/if}
      </div>
    </fieldset>

    <fieldset>
      <legend>Build</legend>
      <div class="field">
        <label class="check">
          <input
            type="checkbox"
            id="i-build"
            bind:checked={build}
            aria-describedby="i-build-hint"
          />
          Build templates from this image
        </label>
        <span class="hint" id="i-build-hint"
          >{#if kind === "lxc"}Off: each create uses the Proxmox template with the agent installer
            added. On: Plumb makes versioned container templates with the Blocks below and fresh
            packages.{:else}Off: a stock image, imported as it is. On: Plumb makes versioned
            templates with the Blocks below and fresh packages.{/if} Only a person can approve a change
          of the recipe, the schedule or the retention.</span
        >
      </div>
      {#if build && kind === "lxc"}
        <p class="banner info" id="i-lxc-keys">
          A container from a built image gets no SSH keys from Proxmox. Add the keys with the Block <span
            class="mono">deploy-user</span
          > on the Target or in this build (D-251).
        </p>
        {#if keyedTargets.length}
          <p class="banner warn" id="i-lxc-keyed-targets">
            The server refuses this build while Targets of this image have SSH keys: <span
              class="mono">{keyedTargets.join(", ")}</span
            >. Remove their keys first (use the Block <span class="mono">deploy-user</span>), then
            switch on the build.
          </p>
        {/if}
      {/if}
      {#if wasBuilt && !build}
        <p class="banner warn" id="i-unbuild">
          Without a build, the plan removes every built version of this image. Guests made from them
          keep running.
        </p>
      {/if}
    </fieldset>

    {#if build}
      <h2>Blocks</h2>
      <p class="muted">
        The build applies these Blocks before the template is made. A Block with a secret parameter
        cannot be used: a template must hold no secret.
      </p>
      {#if secretBlocks.length}
        <p class="muted" id="i-not-offered">
          Not offered: <span class="mono">{secretBlocks.join(", ")}</span>.
        </p>
      {/if}
      {#if unfitBlocks.length}
        <p class="muted" id="i-not-for-kind">
          Not offered for a {kind === "lxc" ? "container" : "virtual machine"}:
          <span class="mono">{unfitBlocks.join(", ")}</span>.
        </p>
      {/if}
      {#each uses as u (u.key)}
        {@const b = blockByName(u.use)}
        {@const unfit = unfitUse(u.use)}
        <fieldset>
          <legend id="iuse-{u.key}-legend" tabindex="-1">{b?.document.spec.title ?? u.use}</legend>
          {#if unfit}<p class="error use-unfit" id="iuse-{u.key}-unfit" tabindex="-1">
              This Block does not fit a {kind === "lxc" ? "container" : "virtual machine"}. {unfit}
              Remove it to save.
            </p>{/if}
          <div class="field">
            <label for="iuse-{u.key}-id">Use id (optional)</label>
            <input
              id="iuse-{u.key}-id"
              type="text"
              bind:value={u.id}
              placeholder={u.use}
              aria-invalid={!!u.errors["__id"]}
              aria-describedby={u.errors["__id"] ? `iuse-${u.key}-id-error` : undefined}
            />
            {#if u.errors["__id"]}<span class="error" id="iuse-{u.key}-id-error"
                >{u.errors["__id"]}</span
              >{/if}
          </div>
          {#if u.fields.length}
            <SchemaFields
              fields={u.fields}
              bind:values={u.values}
              errors={u.errors}
              prefix="iuse-{u.key}"
            />
          {:else}
            <p class="muted">This Block has no parameters.</p>
          {/if}
          <button type="button" class="btn danger" onclick={() => removeBlock(u.key)}>
            Remove {b?.document.spec.title ?? u.use}
          </button>
        </fieldset>
      {:else}
        <p class="muted">No Blocks: the template is the base image with fresh packages.</p>
      {/each}
      <div class="row add">
        <label for="add-choice" class="sr-only">Block to add</label>
        <select id="add-choice" bind:value={addChoice}>
          {#each usable as b (b.document.name)}
            <option value={b.document.name}>{b.document.spec.title ?? b.document.name}</option>
          {/each}
        </select>
        <button type="button" class="btn" id="add-block" onclick={addBlock}>Add Block</button>
      </div>

      <fieldset>
        <legend>{kind === "lxc" ? "Build container" : "Build guest"}</legend>
        <div class="field">
          <label class="check">
            <input
              type="checkbox"
              id="i-upgrade"
              bind:checked={upgrade}
              aria-describedby="i-upgrade-hint"
            />
            Upgrade all packages in the build
          </label>
          <span class="hint" id="i-upgrade-hint"
            >{#if kind === "lxc"}On: before the template is made, the build runs
              <span class="mono">apt-get update</span> and
              <span class="mono">apt-get dist-upgrade</span> in the container (old configuration files
              stay), so the template has what the package mirror serves at that time. apt checks the signatures.
              The build fails when a package is left to upgrade. Off: the packages of the Proxmox template.{:else}On:
              every build installs what the package mirror serves at that time. apt checks the
              signatures. Off: the packages of the base image.{/if}</span
          >
        </div>
        <SchemaFields
          fields={kindGuestFields}
          bind:values={guest}
          errors={{
            ...guestErrors,
            ...(serverErrors["/put/0/spec/build/guest/disk"]
              ? { disk: serverErrors["/put/0/spec/build/guest/disk"] }
              : {}),
          }}
          prefix="i-guest"
        />
      </fieldset>

      <fieldset>
        <legend>Versions and schedule</legend>
        <div class="grid">
          <div class="field">
            <label for="i-keep">Keep</label>
            <input
              id="i-keep"
              type="number"
              min="1"
              max="20"
              bind:value={keep}
              aria-invalid={!!errors["keep"]}
              aria-describedby={desc("i-keep", errors["keep"])}
            />
            <span class="hint" id="i-keep-hint"
              >Newest ready versions that stay (1 … 20). {kind === "lxc"
                ? "One version is a full copy of the root file system of the build container."
                : "One version takes about 1.4 GB (Debian) or 3.3 … 3.9 GB (Ubuntu)."}</span
            >
            {#if errors["keep"]}<span class="error" id="i-keep-error">{errors["keep"]}</span>{/if}
          </div>
          <div class="field">
            <label for="i-every">Build on a schedule</label>
            <select id="i-every" bind:value={every}>
              <option value="none">No schedule</option>
              <option value="day">Every day</option>
              <option value="week">Every week</option>
              <option value="month">Every month</option>
            </select>
          </div>
          {#if every === "week"}
            <div class="field">
              <label for="i-weekday">Day of the week</label>
              <select id="i-weekday" bind:value={weekday}>
                {#each WEEKDAYS as d (d)}<option value={d}>{weekdayName(d)}</option>{/each}
              </select>
            </div>
          {/if}
          {#if every === "month"}
            <div class="field">
              <label for="i-day">Day of the month</label>
              <input
                id="i-day"
                type="number"
                min="1"
                max="28"
                bind:value={day}
                aria-invalid={!!errors["day"]}
                aria-describedby={desc("i-day", errors["day"])}
              />
              <span class="hint" id="i-day-hint">1 … 28, so every month has it.</span>
              {#if errors["day"]}<span class="error" id="i-day-error">{errors["day"]}</span>{/if}
            </div>
          {/if}
          {#if every !== "none"}
            <div class="field">
              <label for="i-at">Time (UTC, 24 h)</label>
              <input
                id="i-at"
                type="time"
                bind:value={at}
                aria-invalid={!!errors["at"]}
                aria-describedby={desc("i-at", errors["at"])}
              />
              <span class="hint" id="i-at-hint"
                >{nextBuild
                  ? `Next build: ${nextBuild.toLocaleString()} (your local time).`
                  : "HH:MM, 24 h, in UTC."}</span
              >
              {#if errors["at"]}<span class="error" id="i-at-error">{errors["at"]}</span>{/if}
            </div>
          {/if}
        </div>
        {#if every !== "none"}
          <p class="muted">
            A scheduled build runs with no click. A failed one does not try again before the next
            slot.
          </p>
        {/if}
      </fieldset>
    {/if}

    <h2>Review</h2>
    <div class="field">
      <label for="i-reason">Reason<span aria-hidden="true"> *</span></label>
      <textarea
        id="i-reason"
        bind:value={reason}
        required
        aria-invalid={!!errors["reason"]}
        aria-describedby={desc("i-reason", errors["reason"])}></textarea>
      <span class="hint" id="i-reason-hint">Stored with the revision (provenance).</span>
      {#if errors["reason"]}<span class="error" id="i-reason-error">{errors["reason"]}</span>{/if}
    </div>
    <ProblemBox error={submitError} />
    <div class="row">
      <button class="btn primary" type="submit" disabled={busy}>Show the plan</button>
      <a
        class="btn"
        href={editing && name ? href({ name: "image", image: name }) : href({ name: "images" })}
        >Cancel</a
      >
    </div>
    <p class="muted">
      {kind === "vm"
        ? "Nothing changes until you approve the plan. A new or changed recipe builds a new version in the Run of that plan."
        : "Nothing changes until you approve the plan."}
    </p>
  </form>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<style>
  .plain-set {
    border: 0;
    padding: 0;
    background: none;
    flex-direction: row;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-4);
  }
  .plain-set legend {
    padding: 0;
    margin-bottom: var(--space-1);
  }
  .plain-set .hint {
    flex-basis: 100%;
  }
  .add {
    margin-bottom: var(--space-4);
  }
  legend:focus {
    outline: 2px solid var(--color-accent);
  }
</style>
