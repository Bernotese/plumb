<script lang="ts">
  import { tick } from "svelte";
  import targetSchema from "../../../internal/state/schema/target.json";
  import { settingsRisks, type PlumbApi } from "../lib/client";
  import { go, href } from "../lib/router";
  import { can, roleNote } from "../lib/session.svelte";
  import DeleteDocument from "../ui/DeleteDocument.svelte";
  import {
    blockParams,
    fieldsOf,
    initialValues,
    mergeParams,
    sameValue,
    toParams,
    pinnedDefaults,
    validate,
    type Field,
    type Values,
  } from "../lib/schemaForm";
  import type { BlockView, ImageView, Issue, TargetSpec, TargetView } from "../lib/types";
  import {
    ALLOW_RESTART_HELP,
    ALLOW_RESTART_HELP_LXC,
    KIND_NAME,
    forKind,
    type GuestKind,
    ALLOW_RESTART_LABEL,
    CREATE_ONLY,
    GUEST_ADDRESS,
    REENROLL_REFUSED,
    REPORTED_TITLE,
    WAITING_ACTIONS,
    formatTime,
    mismatchLine,
    reportedAddrs,
    reportedText,
    waitingLines,
    waitingSince,
  } from "../lib/view";
  import { smallestDisk } from "../lib/images";
  import { unfitText } from "../lib/blocks";
  import AgentCard from "../ui/AgentCard.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";
  import { restored, RESTORED_WAIT } from "../lib/restored.svelte";
  import RawResources from "../ui/RawResources.svelte";
  import SchemaFields from "../ui/SchemaFields.svelte";
  import YamlEditor from "../ui/YamlEditor.svelte";
  import { docPointers } from "../lib/blocks";
  import { pointerErrors } from "../lib/formErrors";
  import { parseDocText, toYaml, type LineError, type Parsed } from "../lib/yaml";

  let { api, name }: { api: PlumbApi; name?: string } = $props();

  // One schema source (D-043): the guest form comes from the Target schema.
  const specSchema = targetSchema.properties.spec;
  // `nics` (T-223) and `disks` (T-239) have their own form parts, so the
  // generic form leaves both out.
  const guestFields: Field[] = fieldsOf(specSchema.properties.guest).filter(
    (f) => f.name !== "nics" && f.name !== "disks",
  );
  const namePattern = new RegExp(targetSchema.properties.name.pattern, "u");

  // --- More NICs (T-223, ARCHITECTURE §20, D-399 … D-404) ---
  const nicSchema = specSchema.properties.guest.properties.nics;
  const MAX_NICS = nicSchema.maxItems;
  const bridgePattern = new RegExp(nicSchema.items.properties.bridge.pattern, "u");
  const cidrPattern = new RegExp(nicSchema.items.properties.ip.pattern, "u");
  const gwPattern = new RegExp(nicSchema.items.properties.gateway.pattern, "u");
  type NicAddr = "none" | "dhcp" | "static";
  interface Nic {
    key: number;
    bridge: string;
    /** "" is untagged. */
    tag: string;
    addr: NicAddr;
    ip: string;
    gateway: string;
  }
  type GuestNic = NonNullable<TargetSpec["guest"]["nics"]>[number];
  let nics = $state<Nic[]>([]);
  let nicErrors = $state<Record<string, string>>({});
  /** The bridges and bridge/tag pairs the install named; null when not known (D-401). */
  let extraNets = $state<{ bridge: string; tag: number | null }[] | null>(null);
  /** M2 of #330: network rights of the token beyond the install. */
  let sdnExtra = $state<{ path: string; role: string }[]>([]);
  /** False when the install named no guest backup storage; null when not known. */
  let backupStorage = $state<boolean | null>(null);
  const extraBridges = $derived([...new Set((extraNets ?? []).map((n) => n.bridge))]);
  const tagsOf = (bridge: string) =>
    (extraNets ?? []).filter((n) => n.bridge === bridge).map((n) => n.tag);
  function toNic(n: GuestNic): Nic {
    return {
      key: nextKey++,
      bridge: n.bridge,
      tag: n.tag === undefined ? "" : String(n.tag),
      addr: n.ip === undefined ? "none" : n.ip === "dhcp" ? "dhcp" : "static",
      ip: n.ip !== undefined && n.ip !== "dhcp" ? n.ip : "",
      gateway: n.gateway ?? "",
    };
  }
  function fromNic(n: Nic): GuestNic {
    return {
      bridge: n.bridge.trim(),
      ...(n.tag.trim() !== "" ? { tag: Number(n.tag) } : {}),
      ...(n.addr === "dhcp" ? { ip: "dhcp" } : {}),
      ...(n.addr === "static" ? { ip: n.ip.trim() } : {}),
      ...(n.addr === "static" && n.gateway.trim() ? { gateway: n.gateway.trim() } : {}),
    };
  }
  async function addNic() {
    const bridge = extraBridges[0] ?? "";
    const tag = tagsOf(bridge)[0];
    nics = [
      ...nics,
      {
        key: nextKey++,
        bridge,
        tag: tag === null || tag === undefined ? "" : String(tag),
        addr: "none",
        ip: "",
        gateway: "",
      },
    ];
    await tick();
    document.getElementById(`nic-${nics.length}-bridge`)?.focus();
  }
  async function removeNic(key: number) {
    nics = nics.filter((n) => n.key !== key);
    await tick();
    document.getElementById("add-nic")?.focus();
  }
  /** Keeps the tag choice valid when the bridge changes. */
  function pickBridge(n: Nic, bridge: string) {
    n.bridge = bridge;
    const tags = tagsOf(bridge);
    if (extraNets && !tags.includes(n.tag === "" ? null : Number(n.tag))) {
      const t = tags[0];
      n.tag = t === null || t === undefined ? "" : String(t);
    }
  }
  function checkNics(): Record<string, string> {
    const e: Record<string, string> = {};
    const gw0 = String(guest["gateway"] ?? "").trim() !== "";
    let gateways = gw0 ? 1 : 0;
    nics.forEach((n, i) => {
      const at = `${i + 1}`;
      const bridge = n.bridge.trim();
      if (!bridgePattern.test(bridge)) e[`${at}/bridge`] = "Give the name of a Proxmox bridge.";
      const tag = n.tag.trim();
      if (tag !== "" && !(/^[0-9]+$/.test(tag) && Number(tag) >= 1 && Number(tag) <= 4094))
        e[`${at}/tag`] = "A VLAN tag is a number from 1 to 4094.";
      if (
        extraNets &&
        !e[`${at}/bridge`] &&
        !e[`${at}/tag`] &&
        !extraNets.some((x) => x.bridge === bridge && x.tag === (tag === "" ? null : Number(tag)))
      )
        e[`${at}/bridge`] =
          tag === ""
            ? `The install did not name the bridge ${bridge} (--extra-bridge).`
            : `The install did not name ${bridge} with tag ${tag} (--extra-vlan).`;
      if (n.addr === "static") {
        if (!cidrPattern.test(n.ip.trim()) || n.ip.trim() === "dhcp")
          e[`${at}/ip`] = "Give an IPv4 address in CIDR form, for example 10.20.0.5/24.";
        if (n.gateway.trim()) {
          gateways += 1;
          if (!gwPattern.test(n.gateway.trim()))
            e[`${at}/gateway`] = "Give an IPv4 address, for example 10.20.0.1.";
          else if (gateways > 1) e[`${at}/gateway`] = "Only one network card may have a gateway.";
        }
      }
    });
    return e;
  }
  // --- Guest backups (T-223, D-408, D-417): the Target field `backup` ---
  type BackupSpec = NonNullable<TargetSpec["backup"]>;
  const backupSchema = specSchema.properties.backup;
  const WEEKDAYS = backupSchema.properties.weekday.enum as BackupSpec["weekday"][];
  let backupOn = $state(false);
  let backupSchedule = $state<BackupSpec["schedule"]>("daily");
  let backupWeekday = $state<NonNullable<BackupSpec["weekday"]>>("sun");
  let backupTime = $state("03:00");
  let backupKeep = $state("7");
  let backupErrors = $state<Record<string, string>>({});
  function loadBackup(b: BackupSpec | undefined) {
    backupOn = b !== undefined;
    backupSchedule = b?.schedule ?? "daily";
    backupWeekday = b?.weekday ?? "sun";
    backupTime = b?.time ?? "03:00";
    backupKeep = String(b?.keep ?? 7);
  }
  function checkBackup(): Record<string, string> {
    const e: Record<string, string> = {};
    if (!backupOn) return e;
    if (!new RegExp(backupSchema.properties.time.pattern, "u").test(backupTime.trim()))
      e["time"] = "Give a time as HH:MM (UTC), for example 03:00.";
    const k = backupKeep.trim();
    if (!(/^[0-9]+$/.test(k) && Number(k) >= 1 && Number(k) <= 30))
      e["keep"] = "Keep 1 to 30 backups.";
    return e;
  }
  function backupSpec(): BackupSpec {
    return {
      schedule: backupSchedule,
      ...(backupSchedule === "weekly" ? { weekday: backupWeekday } : {}),
      time: backupTime.trim(),
      keep: Number(backupKeep.trim()),
    };
  }

  // --- Data disks (T-239, ARCHITECTURE §21, D-423, D-436) ---
  const diskSchema = specSchema.properties.guest.properties.disks;
  const MAX_DISKS = diskSchema.maxItems;
  const SLOTS = Array.from({ length: MAX_DISKS }, (_x, i) => i + 1);
  const DISK_MAX_GB = diskSchema.items.properties.size_gb.maximum;
  const diskPathPattern = new RegExp(diskSchema.items.properties.path.pattern, "u");
  /** The top directories of internal/state/disks.go; a "lib…" top counts too. */
  const SYSTEM_TREES = [
    "bin",
    "boot",
    "dev",
    "etc",
    "proc",
    "root",
    "run",
    "sbin",
    "sys",
    "usr",
    "var",
  ];
  type GuestDisk = NonNullable<TargetSpec["guest"]["disks"]>[number];
  interface DiskRow {
    key: number;
    slot: number;
    size: string;
    path: string;
  }
  /** False: the document has no `disks`, so Plumb does not manage data disks (D-436). */
  let disksManaged = $state(false);
  let disks = $state<DiskRow[]>([]);
  let diskErrors = $state<Record<string, string>>({});
  const freeSlots = (own?: number) =>
    SLOTS.filter((s) => s === own || !disks.some((r) => r.slot === s));
  function toDisk(d: GuestDisk): DiskRow {
    return { key: nextKey++, slot: d.slot, size: String(d.size_gb), path: d.path ?? "" };
  }
  function fromDisk(r: DiskRow): GuestDisk {
    return {
      slot: r.slot,
      size_gb: Number(r.size.trim()),
      ...(kind === "lxc" ? { path: r.path.trim() } : {}),
    };
  }
  async function addDisk() {
    const slot = freeSlots()[0];
    if (slot === undefined) return;
    disksManaged = true;
    disks = [...disks, { key: nextKey++, slot, size: "10", path: "" }];
    await tick();
    document.getElementById(`disk-${disks.length}-size`)?.focus();
  }
  async function removeDisk(key: number) {
    disks = disks.filter((d) => d.key !== key);
    await tick();
    document.getElementById("add-disk")?.focus();
  }
  function diskPathProblem(p: string): string {
    if (!diskPathPattern.test(p))
      return "Give an absolute path of letters, digits, _ . and -, with no . or .. part, for example /srv/data.";
    const parts = p.slice(1).split("/");
    const top = parts[0] ?? "";
    if (SYSTEM_TREES.includes(top) || top.startsWith("lib"))
      return `/${top} is a system tree. Use for example /srv, /opt, /data, /mnt or /home.`;
    return "";
  }
  function checkDisks(): Record<string, string> {
    const e: Record<string, string> = {};
    if (!disksManaged) return e;
    const paths: { i: number; p: string }[] = [];
    disks.forEach((d, idx) => {
      const i = idx + 1;
      const size = d.size.trim();
      const stored = storedDisk(d.slot);
      if (!(/^[0-9]+$/.test(size) && Number(size) >= 1 && Number(size) <= DISK_MAX_GB))
        e[`${i}/size`] = `Give a size from 1 to ${DISK_MAX_GB} GiB.`;
      else if (stored && Number(size) < stored.size_gb)
        e[`${i}/size`] = `A disk only grows: at least ${stored.size_gb} GiB, the size it has now.`;
      if (kind !== "lxc") return;
      const p = d.path.trim();
      const why =
        p === "" ? "A container disk needs a path, for example /srv/data." : diskPathProblem(p);
      if (why) {
        e[`${i}/path`] = why;
        return;
      }
      const other = paths.find(
        (q) => p === q.p || p.startsWith(`${q.p}/`) || q.p.startsWith(`${p}/`),
      );
      if (other) e[`${i}/path`] = `${p} is at or inside the path ${other.p} of disk ${other.i}.`;
      paths.push({ i, p });
    });
    if (removedDisks.length && existing?.status.power === "running" && !allowRestart)
      e["remove"] =
        `Removing a disk of a running guest needs '${forKind(ALLOW_RESTART_LABEL, kind)}' below.`;
    return e;
  }
  const diskHint = (i: number, f: string) =>
    diskErrors[`${i}/${f}`] ? `disk-${i}-${f}-error` : "";

  const nicHint = (i: number, f: string) => (nicErrors[`${i}/${f}`] ? `nic-${i}-${f}-error` : "");
  $effect(() => {
    api.getSettings().then(
      (s) => {
        extraNets = s.proxmox?.extra_nets ?? null;
        backupStorage =
          s.proxmox && s.proxmox.guest_backup !== undefined
            ? s.proxmox.guest_backup !== null
            : null;
        sdnExtra = settingsRisks(s).aclExtra.filter((a) => a.path.startsWith("/sdn/"));
      },
      // Without the settings (a token, an older controller) the server checks the names.
      () => {},
    );
  });
  const editing = $derived(name !== undefined);

  interface Use {
    key: number;
    use: string;
    id: string;
    fields: Field[];
    values: Values;
    /** The stored parameters; keys the form cannot show are kept on save. */
    stored: Record<string, unknown>;
    /** The stored use had a `params` key (an empty one is not added, D-376). */
    hadParams: boolean;
    /** Parameters the person set back to follow the Block default (D-376). */
    followed: string[];
    errors: Record<string, string>;
  }

  let images = $state<ImageView[]>([]);
  let blocks = $state<BlockView[]>([]);
  let existing = $state<TargetView | null>(null);
  /** The disks of the stored Target: their slot, size floor and path are fixed. */
  const storedDisks = $derived<GuestDisk[]>(existing?.document.spec.guest.disks ?? []);
  const storedDisk = (slot: number) => storedDisks.find((d) => d.slot === slot);
  /** Stored disks that the form no longer lists: the Plan deletes them. */
  const removedDisks = $derived(
    disksManaged ? storedDisks.filter((d) => !disks.some((r) => r.slot === d.slot)) : [],
  );
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

  // D-363: the YAML mode. `base` is the stored spec, or the spec last read
  // from the YAML text: the form keeps the keys it does not show from it.
  let base = $state<Partial<TargetSpec>>({});
  const YAML_HINT =
    "The whole Target document. Secrets appear as {secret: name}, never as a value. Saving makes the same plan as the form; the server checks the document.";
  let mode = $state<"form" | "yaml">("form");
  let yamlText = $state("");
  let yamlErrors = $state<LineError[]>([]);
  let yamlParsed = $state.raw<Parsed | null>(null);
  let modeError = $state("");
  const yamlServer = $derived(
    mode === "yaml" && yamlParsed
      ? Object.entries(docPointers(pointerErrors(submitError))).map(([p, message]): LineError => ({
          line: yamlParsed?.lineOf(p) ?? 0,
          pointer: p,
          message,
        }))
      : [],
  );

  // T-071: the open guest:address Issue of this Target, for the mismatch line.
  let addressIssue = $state<Issue | null>(null);
  $effect(() => {
    const t = existing;
    addressIssue = null;
    if (!t || t.status.open_issues === 0) return;
    let stale = false;
    api.listIssues({ target: t.document.name, status: "open" }).then(
      (list) => {
        if (stale) return;
        addressIssue = list.find((i) => i.resource === GUEST_ADDRESS) ?? null;
      },
      // The line is extra information; the page works without it.
      () => {},
    );
    return () => (stale = true);
  });
  const reported = $derived(existing ? reportedAddrs(existing.status) : null);
  const configured = $derived(
    existing
      ? existing.status.ips?.join(", ") ||
          ((existing.document.spec.guest.ip ?? "dhcp") === "dhcp" ? "DHCP" : "–")
      : "",
  );

  // The schema texts speak of VMs and cloud-init; a container field gets
  // its own help (review N1 of #165).
  const LXC_FIELD_HELP: Record<string, string> = {
    disk: "GiB: the size of the root file system, at least 2. Grow only: a grow cannot be undone.",
    ip: "dhcp or an IPv4 address in CIDR form. Create-only. Proxmox writes it into the container at every start.",
    ssh_keys: "Public keys for root; a container has no bootstrap user (D-226). Create-only.",
  };

  // Slice 4 fields (D-291, D-299): short help for people; the schema texts
  // are written for the contract.
  const DAY2_HELP: Record<string, string> = {
    disk: "GiB. Grow only: a grow cannot be undone. The guest must run.",
    power:
      "running or stopped. (none): Plumb does not start or stop the guest. A stop needs the restart setting below.",
    tags: "Proxmox tags, lower case. Plumb keeps exactly these. No entries on a new Target: Plumb does not touch the tags.",
  };

  // D-386: a Target that holds a guest a person made (no Image).
  const adopted = $derived(base.adopted ?? existing?.document.spec.adopted);
  const storedKeep = $derived(existing?.document.spec.backup?.keep);

  // T-194 (D-388): an adopted Target may delete its guest only when the
  // person allowed it with `delete_guest`, by its own plan.
  let allowBusy = $state(false);
  let allowError = $state<unknown>(null);
  let confirmAllow = $state(false);
  async function allowGuestDelete() {
    if (!existing || !existing.document.spec.adopted) return;
    allowBusy = true;
    allowError = null;
    try {
      const spec = $state.snapshot(existing.document.spec);
      if (spec.adopted) spec.adopted.delete_guest = true;
      const plan = await api.createPlan({
        reason: `Allow Plumb to delete the guest ${spec.adopted?.vmid} with the Target ${existing.document.name}.`,
        put: [{ kind: "Target", name: existing.document.name, spec }],
      });
      go({ name: "plan", id: plan.id });
    } catch (err) {
      allowError = err;
    } finally {
      allowBusy = false;
    }
  }

  // The type follows the Image (T-038 §8); a stored Target keeps its own.
  const kind = $derived<GuestKind>(
    existing?.document.spec.type ??
      images.find((i) => i.document.name === image)?.document.spec.type ??
      "vm",
  );
  // A container from a built Image is a clone: Proxmox writes no SSH keys
  // into it, so the Target has none; the Block deploy-user adds them (D-251).
  const builtLxc = $derived(
    kind === "lxc" &&
      images.find((i) => i.document.name === image)?.document.spec.build !== undefined,
  );
  const deployUser = $derived(blocks.some((b) => b.document.name === "deploy-user"));
  // N2 of the #176 review: keys typed for a stock Image are not sent for a
  // built one; say it, do not drop them without a word.
  const droppedKeys = $derived(
    builtLxc && !editing && Array.isArray(guest["ssh_keys"])
      ? (guest["ssh_keys"] as unknown[]).filter((k) => String(k).trim() !== "").length
      : 0,
  );
  const usesDeployUser = $derived(uses.some((u) => u.use === "deploy-user"));
  const day2Fields = guestFields.map((f) =>
    DAY2_HELP[f.name] ? { ...f, description: DAY2_HELP[f.name] } : f,
  );
  // A container has no bootstrap user; its SSH keys go to root (D-226).
  const kindFields = $derived(
    kind === "lxc"
      ? day2Fields
          .filter((f) => f.name !== "user" && !(builtLxc && f.name === "ssh_keys"))
          .map((f) => {
            const description = LXC_FIELD_HELP[f.name];
            const label = f.name === "ssh_keys" ? "SSH keys for root" : f.label;
            return description ? { ...f, label, description } : { ...f, label };
          })
      : day2Fields,
  );
  // A container takes cores and memory live: no "Restart needed" (D-248).
  const waitingFrom = $derived(
    kind === "lxc" ? null : waitingSince(existing?.status.waiting ?? []),
  );
  // The stored setting decides the re-enrollment, not the unsaved checkbox.
  const storedAllow = $derived(existing?.document.spec.allow_restart === true);

  // D-192, D-220, D-223: a stock Ubuntu 26.04 Image can sometimes stay on DHCP until
  // its first restart; an Image built by Plumb does not have this limit.
  const addressNote = $derived.by(() => {
    const ip = String(guest["ip"] ?? "dhcp");
    const img = images.find((i) => i.document.name === image);
    const label = `${image} ${img?.document.spec.title ?? ""}`;
    if (editing || ip === "dhcp" || !/ubuntu/i.test(label) || !/26\.04/.test(label)) return {};
    return {
      ip: "A stock Ubuntu 26.04 image can sometimes stay on DHCP until its first restart (D-192, D-220). Use DHCP or an image built by Plumb (D-223).",
    } as Record<string, string>;
  });

  // D-202: a Target from a built image needs at least the disk of its template.
  const diskNote = $derived.by(() => {
    const min = smallestDisk(images.find((i) => i.document.name === image)?.document.spec);
    if (editing || min === null) return {} as Record<string, string>;
    return { disk: `At least ${min} GiB: the template of image ${image} has ${min} GiB.` };
  });

  const blockByName = (n: string) => blocks.find((b) => b.document.name === n);

  // T-118 (D-260): the picker offers only Blocks that fit the guest type.
  const addable = $derived(blocks.filter((b) => unfitText(b.document, kind) === ""));
  const hidden = $derived(
    blocks.filter((b) => unfitText(b.document, kind) !== "").map((b) => b.document.name),
  );
  $effect(() => {
    if (!addable.some((b) => b.document.name === addChoice))
      addChoice = addable[0]?.document.name ?? "";
  });
  const unfitUse = (u: Use) => {
    const b = blockByName(u.use);
    return b ? unfitText(b.document, kind) : "";
  };

  function makeUse(use: string, id = "", params?: Record<string, unknown>): Use {
    const schema = blockByName(use)?.document.spec.params;
    const fields = fieldsOf(schema);
    return {
      key: nextKey++,
      use,
      id,
      fields,
      values: initialValues(fields, params ?? {}),
      stored: params ?? {},
      hadParams: params !== undefined,
      followed: [],
      errors: {},
    };
  }

  /**
   * #283 n1: an object parameter whose parts have defaults that its own
   * default leaves out never equals that default in the form, so a save
   * always writes it (D-376 cannot follow the default for it). Say so.
   */
  function objectNotes(u: Use): Record<string, string> {
    const out: Record<string, string> = {};
    for (const f of u.fields) {
      if (f.kind !== "object" || f.default === undefined || u.values[f.name] == null) continue;
      const filled = toParams([f], initialValues([f], {}))[f.name];
      if (!sameValue(filled, f.default))
        out[f.name] =
          "Saved with all its parts: they have defaults of their own that the Block default leaves out. A later change of the Block default does not reach this Target.";
    }
    return out;
  }

  /** D-376: the person follows the Block default again for one parameter. */
  function follow(u: Use, name: string) {
    const f = u.fields.find((x) => x.name === name);
    if (!f) return;
    u.followed = [...u.followed, name];
    u.values[name] = initialValues([f], {})[name] ?? "";
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
        if (t) {
          targetName = t.document.name;
          loadSpec(t.document.spec);
        } else {
          image = i[0]?.document.name ?? "";
          guest = initialValues(guestFields, {
            cores: 2,
            memory: 2048,
            disk: 20,
            bridge: "vmbr0",
            ip: "dhcp",
            // D-299: a new Target is managed as running; a stored one keeps "not managed".
            power: "running",
          });
        }
        loaded = true;
      },
      (e: unknown) => (loadError = e),
    );
  });

  /** Fills the form from a spec: the stored one, or one read from YAML. */
  function loadSpec(spec: Partial<TargetSpec>) {
    base = spec;
    if (typeof spec.image === "string") image = spec.image;
    guest = initialValues(guestFields, (spec.guest ?? {}) as Record<string, unknown>);
    nics = (spec.guest?.nics ?? []).map(toNic);
    disksManaged = spec.guest?.disks !== undefined;
    disks = (spec.guest?.disks ?? []).map(toDisk);
    loadBackup(spec.backup);
    allowRestart = spec.allow_restart === true;
    uses = (spec.blocks ?? []).map((u) => makeUse(u.use, u.id ?? "", u.params));
  }

  function addBlock(use = addChoice) {
    if (!use) return;
    uses = [...uses, makeUse(use)];
    const key = uses[uses.length - 1]?.key;
    queueMicrotask(() => document.getElementById(`use-${key}-legend`)?.focus());
  }

  function removeBlock(key: number) {
    uses = uses.filter((u) => u.key !== key);
    queueMicrotask(() => document.getElementById("add-block")?.focus());
  }

  function check(needReason = true): boolean {
    const e: Record<string, string> = {};
    if (!editing && !namePattern.test(targetName)) {
      e["name"] =
        "Use lower-case letters, digits, '-' and '.'; start and end with a letter or digit.";
    }
    if (!image && !adopted) e["image"] = "Choose an image.";
    if (needReason && !reason.trim()) e["reason"] = "Say why you make this change.";
    guestErrors = validate(kindFields, guest);
    // D-202: the disk of a Target from a built image cannot be smaller than the template.
    const min = smallestDisk(images.find((i) => i.document.name === image)?.document.spec);
    if (!editing && min !== null && !guestErrors["disk"] && Number(guest["disk"]) < min)
      guestErrors = {
        ...guestErrors,
        disk: `At least ${min} GiB: the template of image ${image} has ${min} GiB, and a disk cannot shrink.`,
      };
    // D-291, D-299: the disk only grows; a stop needs allow_restart.
    const storedDisk = existing?.document.spec.guest.disk;
    if (
      editing &&
      storedDisk !== undefined &&
      !guestErrors["disk"] &&
      Number(guest["disk"]) < storedDisk
    )
      guestErrors = {
        ...guestErrors,
        disk: `A disk only grows: at least ${storedDisk} GiB, the size it has now.`,
      };
    if (guest["power"] === "stopped" && !allowRestart && !guestErrors["power"])
      guestErrors = {
        ...guestErrors,
        power: `A stop needs '${forKind(ALLOW_RESTART_LABEL, kind)}' below.`,
      };
    nicErrors = checkNics();
    diskErrors = checkDisks();
    backupErrors = checkBackup();
    const seen: string[] = [];
    for (const u of uses) {
      u.errors = validate(u.fields, u.values);
      const id = u.id.trim() || u.use;
      if (seen.includes(id)) u.errors["__id"] = `Two uses of a Block need different ids ("${id}").`;
      else if (u.id.trim() && !namePattern.test(u.id.trim()))
        u.errors["__id"] = "The id has the wrong format.";
      seen.push(id);
      const unfit = unfitUse(u);
      if (unfit) u.errors["__kind"] = unfit;
    }
    errors = e;
    return (
      Object.keys(e).length === 0 &&
      Object.keys(guestErrors).length === 0 &&
      Object.keys(nicErrors).length === 0 &&
      Object.keys(diskErrors).length === 0 &&
      Object.keys(backupErrors).length === 0 &&
      uses.every((u) => Object.keys(u.errors).length === 0)
    );
  }

  const focusInvalid = () =>
    queueMicrotask(() =>
      (document.querySelector('[aria-invalid="true"], .use-unfit') as HTMLElement | null)?.focus(),
    );

  /** The spec that the form saves: `base` with the form values. */
  function buildSpec(): TargetSpec {
    const known = ["type", "image", "guest", "blocks", "overrides", "allow_restart", "backup"];
    const extra = Object.fromEntries(Object.entries(base).filter(([k]) => !known.includes(k)));
    const guestSpec = mergeParams(
      (base.guest ?? {}) as Record<string, unknown>,
      // On an existing target the create-only fields keep their stored
      // value: a schema default the form shows must not become a change.
      editing
        ? kindFields.filter((f) => !(CREATE_ONLY as readonly string[]).includes(f.name))
        : kindFields,
      guest,
    );
    // D-299: a missing `tags` means "not managed"; an emptied list means "no tags".
    if (base.guest?.tags !== undefined && guestSpec["tags"] === undefined) guestSpec["tags"] = [];
    // D-399: no key without a card; removing every card removes the key.
    if (nics.length) guestSpec["nics"] = nics.map(fromNic);
    else delete guestSpec["nics"];
    // D-436: no `disks` means "not managed"; removing the field deletes nothing.
    if (disksManaged && !adopted) guestSpec["disks"] = disks.map(fromDisk);
    else delete guestSpec["disks"];
    return {
      ...extra,
      type: kind,
      // An adopted Target has no Image (D-386); `adopted` comes from `base`.
      ...(adopted ? {} : { image }),
      guest: guestSpec as TargetSpec["guest"],
      blocks: uses.map((u) => {
        // D-376: only what the person set; the server fills the defaults.
        const params = blockParams(u.stored, u.fields, u.values, u.followed);
        return {
          use: u.use,
          ...(u.id.trim() ? { id: u.id.trim() } : {}),
          ...(Object.keys(params).length || u.hadParams ? { params } : {}),
        };
      }),
      // D-376: an empty overrides is not added; a stored one stays.
      ...(base.overrides !== undefined ? { overrides: base.overrides } : {}),
      // Written only when set or stored before, so an old Target gets no
      // new key by an unrelated edit (default false, D-194).
      ...(allowRestart || base.allow_restart !== undefined ? { allow_restart: allowRestart } : {}),
      // D-417 (3): removing `backup` stops the jobs and deletes no backup.
      ...(backupOn ? { backup: backupSpec() } : {}),
    };
  }

  const yamlWant = () => ({
    kind: "Target",
    ...(editing && name ? { name } : {}),
    namePattern,
  });

  /** Form → YAML: the document that the form would save. */
  async function showYaml() {
    modeError = "";
    if (mode === "yaml") return;
    if (!check(false)) {
      modeError = "Fix the marked fields first. Then switch to YAML.";
      focusInvalid();
      return;
    }
    yamlText = toYaml({ kind: "Target", name: targetName, spec: buildSpec() });
    yamlErrors = [];
    yamlParsed = null;
    submitError = null;
    mode = "yaml";
    await tick();
    document.getElementById("t-yaml")?.focus();
  }

  /** YAML → form: read the text into the form; on an error stay in YAML. */
  async function showForm() {
    modeError = "";
    if (mode === "form") return;
    const r = parseDocText(yamlText, yamlWant());
    yamlParsed = r.parsed;
    yamlErrors = r.errors;
    if (!r.doc) {
      modeError = "The YAML text has problems. Fix them to switch back to the form.";
      await tick();
      document.getElementById("t-yaml-errors")?.focus();
      return;
    }
    if (!editing) targetName = r.doc.name;
    loadSpec(r.doc.spec as Partial<TargetSpec>);
    submitError = null;
    mode = "form";
  }

  async function submit(ev: SubmitEvent) {
    ev.preventDefault();
    submitError = null;
    let put: { kind: "Target"; name: string; spec: TargetSpec };
    if (mode === "yaml") {
      // The same proposal as the form: the server checks the document (D-363).
      const r = parseDocText(yamlText, yamlWant());
      yamlParsed = r.parsed;
      yamlErrors = r.errors;
      errors = reason.trim() ? {} : { reason: "Say why you make this change." };
      if (!r.doc || errors["reason"]) {
        await tick();
        document.getElementById(r.doc ? "t-reason" : "t-yaml-errors")?.focus();
        return;
      }
      put = { kind: "Target", name: r.doc.name, spec: r.doc.spec as unknown as TargetSpec };
    } else {
      if (!check()) {
        focusInvalid();
        return;
      }
      put = { kind: "Target", name: targetName, spec: buildSpec() };
    }
    busy = true;
    try {
      const plan = await api.createPlan({ reason, put: [put] });
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
{#if existing}
  <p class="sub-links">
    <a href={href({ name: "target-agent", target: existing.document.name })}>Agent</a> ·
    <a href={href({ name: "target-backups", target: existing.document.name })} id="backups-link"
      >Backups</a
    >
  </p>
{/if}
<ProblemBox error={loadError} />
{#if existing}
  <section class="card addr-card" class:mismatch={addressIssue} aria-labelledby="addr-title">
    <h2 id="addr-title">Address</h2>
    {#if addressIssue}
      <p class="warn" id="addr-mismatch">
        {mismatchLine(addressIssue)} ·
        <a href={href({ name: "issue", id: addressIssue.id })}>Issue {addressIssue.id}</a>
      </p>
    {/if}
    <dl class="addrs">
      <dt>Configured</dt>
      <dd class="mono">{configured}</dd>
      <dt>Reported <span class="mark">by the guest</span></dt>
      <dd aria-describedby="addr-help">
        {#if reported}
          <span class="mono">{reportedText(reported)}</span>
          <span class="muted"> · at {formatTime(existing.status.reported_at)}</span>
        {:else}
          <span class="muted">No list: the agent is offline or does not report addresses.</span>
        {/if}
      </dd>
    </dl>
    <p class="hint" id="addr-help">{REPORTED_TITLE}</p>
  </section>
{/if}
{#if existing?.status.agent_update === "required"}
  <p class="banner danger" id="agent-required">
    Plumb refuses every plan for this Target until its agent is updated.
    <a href={href({ name: "target-agent", target: existing.document.name })}
      >Update the agent of {existing.document.name}</a
    >
  </p>
{/if}
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
    <div class="modes" role="group" aria-label="Edit the target as">
      <button
        type="button"
        class="btn"
        id="t-mode-form"
        aria-pressed={mode === "form"}
        onclick={showForm}>Form</button
      >
      <button
        type="button"
        class="btn"
        id="t-mode-yaml"
        aria-pressed={mode === "yaml"}
        onclick={showYaml}>YAML</button
      >
    </div>
    {#if modeError}<p class="error" id="t-mode-error" role="alert">{modeError}</p>{/if}
    {#if mode === "yaml"}
      <YamlEditor
        id="t-yaml"
        label="Target (YAML)"
        bind:text={yamlText}
        errors={yamlErrors}
        serverErrors={yamlServer}
        hint={YAML_HINT}
      />
    {:else}
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
              aria-describedby={errors["name"]
                ? "t-name-error"
                : editing
                  ? "t-name-hint"
                  : undefined}
            />
            {#if editing}<span class="hint" id="t-name-hint">The name cannot change.</span>{/if}
            {#if errors["name"]}<span class="error" id="t-name-error">{errors["name"]}</span>{/if}
          </div>
          <div class="field">
            {#if adopted}
              <span class="label">Image</span>
              <span id="t-adopted"
                >None: adopted guest <span class="mono">{adopted.vmid}</span> on
                <span class="mono">{adopted.node}</span>. Plumb did not make it.</span
              >
            {:else}
              <label for="t-image">Image</label>
              <select
                id="t-image"
                bind:value={image}
                disabled={editing}
                aria-describedby={editing ? "t-image-hint" : undefined}
              >
                {#each images as i (i.document.name)}
                  <option value={i.document.name}>{i.document.spec.title ?? i.document.name}</option
                  >
                {/each}
              </select>
              {#if editing}<span class="hint" id="t-image-hint"
                  >{#if existing?.status.image_version}<span id="t-image-version" class="block"
                      >Made from image {existing.document.spec.image} v{existing.status
                        .image_version}.</span
                    >
                  {/if}The image is used only to create the guest.</span
                >{/if}
            {/if}
          </div>
          <div class="field">
            <span class="label">Type</span>
            <span id="t-type">{KIND_NAME[kind]}</span>
          </div>
        </div>
      </fieldset>

      <fieldset>
        <legend>Guest settings</legend>
        <SchemaFields
          fields={kindFields}
          bind:values={guest}
          errors={guestErrors}
          prefix="guest"
          readonly={editing ? CREATE_ONLY : []}
          readonlyHint="Create-only: it cannot change on an existing target."
          notes={{ ...addressNote, ...diskNote }}
        />
        {#if builtLxc}
          <div class="banner info" id="t-lxc-keys">
            <p>
              A container from the built image <span class="mono">{image}</span> gets no SSH keys
              from Proxmox. Add the keys with the Block <span class="mono">deploy-user</span> (D-251).
            </p>
            {#if droppedKeys}
              <p class="warn" id="t-keys-dropped">
                The {droppedKeys === 1 ? "SSH key" : `${droppedKeys} SSH keys`} you typed for the other
                image {droppedKeys === 1 ? "is" : "are"} not sent. Put {droppedKeys === 1
                  ? "it"
                  : "them"} into the Block <span class="mono">deploy-user</span>.
              </p>
            {/if}
            {#if deployUser && !usesDeployUser}
              <button
                type="button"
                class="btn"
                id="t-add-deploy-user"
                onclick={() => addBlock("deploy-user")}>Add Block deploy-user</button
              >
            {:else if !deployUser}
              <p class="muted">There is no Block <span class="mono">deploy-user</span> yet.</p>
            {/if}
          </div>
        {/if}
        <div class="field">
          <label class="check">
            <input
              type="checkbox"
              id="t-allow-restart"
              bind:checked={allowRestart}
              aria-describedby="t-allow-restart-hint"
            />
            {forKind(ALLOW_RESTART_LABEL, kind)}
          </label>
          <span class="hint" id="t-allow-restart-hint"
            >{kind === "lxc" ? ALLOW_RESTART_HELP_LXC : ALLOW_RESTART_HELP}</span
          >
        </div>
      </fieldset>

      <fieldset id="nics" aria-describedby="nics-hint">
        <legend id="nics-legend">More network cards</legend>
        <p class="hint" id="nics-hint">
          The card above is the first one. Up to {MAX_NICS} more; Plumb chooses their MAC addresses. Each
          bridge, and each bridge with a VLAN tag, must be named when Plumb is installed. Adding or removing
          a card, or changing its bridge or tag, is disruptive.
        </p>
        {#if sdnExtra.length}
          <div class="banner danger" role="note" id="nics-acl-extra">
            <p>
              <strong>The token may use networks the install did not name.</strong> Run the installer
              again with the same flags (a repair run); it removes these rights.
            </p>
            <ul>
              {#each sdnExtra as a (a.path + a.role)}<li>
                  <code>{a.path}</code> ({a.role})
                </li>{/each}
            </ul>
          </div>
        {/if}
        {#if adopted}
          <p class="muted" id="nics-adopted">
            An adopted Target keeps the network cards that the guest has. Plumb adds none.
          </p>
        {:else}
          {#if extraNets !== null && extraNets.length === 0}
            <p class="muted" id="nics-none-named">
              The install named no other bridge. Run the installer again with
              <code>--extra-bridge</code> or <code>--extra-vlan</code> first.
            </p>
          {/if}
          {#each nics as n, idx (n.key)}
            {@const i = idx + 1}
            {@const tags = tagsOf(n.bridge)}
            <fieldset class="nic" aria-labelledby="nic-{i}-legend">
              <legend id="nic-{i}-legend">Network card {i + 1}</legend>
              <div class="field">
                <label for="nic-{i}-bridge">Bridge</label>
                {#if extraNets !== null && (extraBridges.includes(n.bridge) || n.bridge === "")}
                  <select
                    id="nic-{i}-bridge"
                    value={n.bridge}
                    onchange={(ev) => pickBridge(n, ev.currentTarget.value)}
                    aria-invalid={!!nicErrors[`${i}/bridge`]}
                    aria-describedby={nicHint(i, "bridge") || undefined}
                  >
                    {#each extraBridges as b (b)}<option value={b}>{b}</option>{/each}
                  </select>
                {:else}
                  <input
                    id="nic-{i}-bridge"
                    type="text"
                    class="mono"
                    bind:value={n.bridge}
                    aria-invalid={!!nicErrors[`${i}/bridge`]}
                    aria-describedby={nicHint(i, "bridge") || undefined}
                  />
                {/if}
                {#if nicErrors[`${i}/bridge`]}<span class="error" id="nic-{i}-bridge-error"
                    >{nicErrors[`${i}/bridge`]}</span
                  >{/if}
              </div>
              <div class="field">
                <label for="nic-{i}-tag">VLAN tag</label>
                {#if extraNets !== null && extraBridges.includes(n.bridge)}
                  <select
                    id="nic-{i}-tag"
                    bind:value={n.tag}
                    aria-invalid={!!nicErrors[`${i}/tag`]}
                    aria-describedby={nicHint(i, "tag") || undefined}
                  >
                    {#each tags as t (t)}<option value={t === null ? "" : String(t)}
                        >{t === null ? "(untagged)" : t}</option
                      >{/each}
                  </select>
                {:else}
                  <input
                    id="nic-{i}-tag"
                    type="text"
                    inputmode="numeric"
                    bind:value={n.tag}
                    placeholder="(untagged)"
                    aria-invalid={!!nicErrors[`${i}/tag`]}
                    aria-describedby={nicHint(i, "tag") || undefined}
                  />
                {/if}
                {#if nicErrors[`${i}/tag`]}<span class="error" id="nic-{i}-tag-error"
                    >{nicErrors[`${i}/tag`]}</span
                  >{/if}
              </div>
              <div class="field">
                <label for="nic-{i}-addr">Address</label>
                <select id="nic-{i}-addr" bind:value={n.addr}>
                  <option value="none">none (the card is up without an address)</option>
                  <option value="dhcp">DHCP</option>
                  <option value="static">static</option>
                </select>
              </div>
              {#if n.addr === "static"}
                <div class="field">
                  <label for="nic-{i}-ip">IPv4 address (CIDR)</label>
                  <input
                    id="nic-{i}-ip"
                    type="text"
                    class="mono"
                    bind:value={n.ip}
                    placeholder="10.20.0.5/24"
                    aria-invalid={!!nicErrors[`${i}/ip`]}
                    aria-describedby={nicHint(i, "ip") || undefined}
                  />
                  {#if nicErrors[`${i}/ip`]}<span class="error" id="nic-{i}-ip-error"
                      >{nicErrors[`${i}/ip`]}</span
                    >{/if}
                </div>
                <div class="field">
                  <label for="nic-{i}-gateway">Gateway (optional)</label>
                  <input
                    id="nic-{i}-gateway"
                    type="text"
                    class="mono"
                    bind:value={n.gateway}
                    aria-invalid={!!nicErrors[`${i}/gateway`]}
                    aria-describedby="nic-{i}-gateway-hint {nicHint(i, 'gateway')}"
                  />
                  <span class="hint" id="nic-{i}-gateway-hint"
                    >At most one gateway over all network cards.</span
                  >
                  {#if nicErrors[`${i}/gateway`]}<span class="error" id="nic-{i}-gateway-error"
                      >{nicErrors[`${i}/gateway`]}</span
                    >{/if}
                </div>
              {/if}
              <button type="button" class="btn danger" onclick={() => removeNic(n.key)}
                >Remove network card {i + 1}</button
              >
            </fieldset>
          {/each}
          {#if nics.length < MAX_NICS}
            <button
              type="button"
              class="btn"
              id="add-nic"
              onclick={addNic}
              disabled={extraNets !== null && extraNets.length === 0}>Add network card</button
            >
          {/if}
        {/if}
      </fieldset>

      <fieldset id="disks" aria-describedby="disks-hint">
        <legend id="disks-legend">Data disks</legend>
        <p class="hint" id="disks-hint">
          The disk above is the main disk. Up to {MAX_DISKS} data disks more, each on the disk storage
          named at install. The slot is the disk's fixed name: removing one disk never renames another.
          A disk only grows. Adding a disk is reversible.
          {#if kind === "lxc"}
            Proxmox makes a file system on a container disk and mounts it at its path; the path
            cannot change later. Guest backups hold it.
          {:else}
            A VM data disk is a raw block device with the serial <code>plumb-N</code>: find it with
            <code>lsblk -o NAME,SERIAL</code>, then format and mount it with a Block or by hand.
          {/if}
        </p>
        {#if adopted}
          <p class="muted" id="disks-adopted">
            An adopted Target keeps the disks that the guest has. Plumb adds none.
          </p>
        {:else}
          <div class="field checkbox">
            <label>
              <input type="checkbox" id="disks-managed" bind:checked={disksManaged} />
              Plumb manages the data disks of this guest
            </label>
            <span class="hint" id="disks-managed-hint">
              {#if disksManaged}
                Plumb owns slots 1 to {MAX_DISKS}: a disk in a slot that the list does not name is
                removed by the Plan, and a disk that a person adds is drift.
              {:else}
                Off: Plumb does not touch data disks, and a disk that a person adds is no drift.
                {#if storedDisks.length}Switching it off deletes nothing: the disks stay in the
                  guest, no longer managed.{/if}
              {/if}
            </span>
          </div>
          {#if disksManaged}
            {#each disks as d, idx (d.key)}
              {@const i = idx + 1}
              {@const stored = storedDisk(d.slot)}
              <fieldset class="disk" aria-labelledby="disk-{i}-legend">
                <legend id="disk-{i}-legend">Data disk in slot {d.slot}</legend>
                <div class="field">
                  <label for="disk-{i}-slot">Slot</label>
                  {#if stored}
                    <span id="disk-{i}-slot" class="mono"
                      >{d.slot} ({kind === "lxc" ? `mp${d.slot}` : `scsi${d.slot}`})</span
                    >
                  {:else}
                    <select id="disk-{i}-slot" bind:value={d.slot}>
                      {#each freeSlots(d.slot) as sl (sl)}
                        <option value={sl}>{sl} ({kind === "lxc" ? `mp${sl}` : `scsi${sl}`})</option
                        >
                      {/each}
                    </select>
                  {/if}
                </div>
                <div class="field">
                  <label for="disk-{i}-size">Size (GiB)</label>
                  <input
                    id="disk-{i}-size"
                    type="text"
                    inputmode="numeric"
                    bind:value={d.size}
                    aria-invalid={!!diskErrors[`${i}/size`]}
                    aria-describedby={diskHint(i, "size") || undefined}
                  />
                  {#if diskErrors[`${i}/size`]}<span class="error" id="disk-{i}-size-error"
                      >{diskErrors[`${i}/size`]}</span
                    >{/if}
                </div>
                {#if kind === "lxc"}
                  <div class="field">
                    <label for="disk-{i}-path">Mount path</label>
                    {#if stored}
                      <span id="disk-{i}-path" class="mono">{d.path}</span>
                      <span class="hint">Create-only. To move the data, move it in the guest.</span>
                    {:else}
                      <input
                        id="disk-{i}-path"
                        type="text"
                        class="mono"
                        placeholder="/srv/data"
                        bind:value={d.path}
                        aria-invalid={!!diskErrors[`${i}/path`]}
                        aria-describedby={diskHint(i, "path") || undefined}
                      />
                    {/if}
                    {#if diskErrors[`${i}/path`]}<span class="error" id="disk-{i}-path-error"
                        >{diskErrors[`${i}/path`]}</span
                      >{/if}
                  </div>
                {/if}
                <button type="button" class="btn danger" onclick={() => removeDisk(d.key)}
                  >Remove the disk in slot {d.slot}</button
                >
              </fieldset>
            {/each}
            {#if removedDisks.length}
              <div class="banner danger" role="note" id="disks-removed">
                <p>
                  <strong
                    >The Plan deletes {removedDisks.length === 1 ? "this disk" : "these disks"}: the
                    data is lost.</strong
                  >
                </p>
                <ul>
                  {#each removedDisks as r (r.slot)}<li>
                      Slot {r.slot}: {r.size_gb} GiB{r.path ? ` at ${r.path}` : ""}
                    </li>{/each}
                </ul>
                <p>
                  The Plan shows it as destructive, and a person approves it. Plumb detaches the
                  disk first, then frees its volume in a second step of the same Run.
                </p>
                {#if diskErrors["remove"]}<p class="error" id="disks-remove-error">
                    {diskErrors["remove"]}
                  </p>{/if}
              </div>
            {/if}
            {#if disks.length < MAX_DISKS}
              <button type="button" class="btn" id="add-disk" onclick={addDisk}
                >Add data disk</button
              >
            {/if}
          {:else if !storedDisks.length}
            <button type="button" class="btn" id="add-disk" onclick={addDisk}>Add data disk</button>
          {/if}
          <p class="note" id="disks-restore">
            A restore of a backup made before a disk was added:
            {#if kind === "lxc"}
              a container loses the data on that disk.
            {:else}
              a VM keeps that disk as an unused volume (free it by hand); the next Run attaches a
              new empty disk in its slot.
            {/if}
            The restore Plan names the disk, and the safety backup holds it.
          </p>
        {/if}
      </fieldset>

      <fieldset id="backup" aria-describedby="backup-hint">
        <legend>Guest backups</legend>
        <p class="hint" id="backup-hint">
          Plumb backs up the guest to the backup storage named at install. A backup does not change
          the guest. Without a schedule, Plumb makes backups only with "Back up now".
          {#if existing}<a href={href({ name: "target-backups", target: existing.document.name })}
              >Backups of {existing.document.name}</a
            >{/if}
        </p>
        {#if backupStorage === false}
          <p class="note" id="backup-no-storage">
            The install named no guest backup storage: a schedule makes no backup until it does (<code
              >--guest-backup-storage</code
            >).
          </p>
        {/if}
        <div class="field">
          <label class="check">
            <input type="checkbox" id="backup-on" bind:checked={backupOn} />
            Back up this guest on a schedule
          </label>
        </div>
        {#if backupOn}
          <div class="field">
            <label for="backup-schedule">Schedule</label>
            <select id="backup-schedule" bind:value={backupSchedule}>
              <option value="daily">daily</option>
              <option value="weekly">weekly</option>
            </select>
          </div>
          {#if backupSchedule === "weekly"}
            <div class="field">
              <label for="backup-weekday">Weekday</label>
              <select id="backup-weekday" bind:value={backupWeekday}>
                {#each WEEKDAYS as d (d)}<option value={d}>{d}</option>{/each}
              </select>
            </div>
          {/if}
          <div class="field">
            <label for="backup-time">Time (UTC)</label>
            <input
              id="backup-time"
              type="text"
              class="mono"
              bind:value={backupTime}
              aria-invalid={!!backupErrors["time"]}
              aria-describedby={backupErrors["time"] ? "backup-time-error" : undefined}
            />
            {#if backupErrors["time"]}<span class="error" id="backup-time-error"
                >{backupErrors["time"]}</span
              >{/if}
          </div>
          <div class="field">
            <label for="backup-keep">Keep</label>
            <input
              id="backup-keep"
              type="text"
              inputmode="numeric"
              bind:value={backupKeep}
              aria-invalid={!!backupErrors["keep"]}
              aria-describedby="backup-keep-hint{backupErrors['keep'] ? ' backup-keep-error' : ''}"
            />
            <span class="hint" id="backup-keep-hint"
              >How many scheduled backups Plumb keeps (1 to 30). Apart from them it keeps at most 2
              made by "Back up now".</span
            >
            {#if storedKeep !== undefined && Number(backupKeep) < storedKeep}
              <span class="hint note warn" id="backup-keep-lower"
                >Lower than now ({storedKeep}): when the next backup job would delete backups with
                this value, the plan is destructive and says how many go.</span
              >
            {/if}
            {#if backupErrors["keep"]}<span class="error" id="backup-keep-error"
                >{backupErrors["keep"]}</span
              >{/if}
          </div>
        {:else if existing?.document.spec.backup}
          <p class="note" id="backup-off-note">
            Without a schedule Plumb stops the backups of this guest. It deletes none: the backups
            stay until a person deletes them.
          </p>
        {/if}
      </fieldset>

      <h2>Blocks</h2>
      {#each uses as u (u.key)}
        {@const b = blockByName(u.use)}
        {@const unfit = unfitUse(u)}
        <fieldset aria-describedby={b?.document.spec.description ? `use-${u.key}-desc` : undefined}>
          <legend id="use-{u.key}-legend" tabindex="-1">{b?.document.spec.title ?? u.use}</legend>
          {#if b?.document.spec.description}<p class="muted" id="use-{u.key}-desc">
              {b.document.spec.description}
            </p>{/if}
          {#if unfit}<p class="error use-unfit" id="use-{u.key}-unfit" tabindex="-1">
              This Block does not fit a {kind === "lxc" ? "container" : "virtual machine"}. {unfit}
              Remove it to save.
            </p>{/if}
          <div class="field">
            <label for="use-{u.key}-id">Use id (optional)</label>
            <input
              id="use-{u.key}-id"
              type="text"
              bind:value={u.id}
              placeholder={u.use}
              aria-invalid={!!u.errors["__id"]}
              aria-describedby="use-{u.key}-id-hint{u.errors['__id']
                ? ` use-${u.key}-id-error`
                : ''}"
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
              notes={objectNotes(u)}
              pinned={pinnedDefaults(u.stored, u.fields, u.values, u.followed)}
              onfollow={(n) => follow(u, n)}
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
          {#each addable as b (b.document.name)}
            <option value={b.document.name}>{b.document.spec.title ?? b.document.name}</option>
          {/each}
        </select>
        <button
          type="button"
          class="btn"
          id="add-block"
          disabled={addable.length === 0}
          onclick={() => addBlock()}>Add Block</button
        >
      </div>
      {#if hidden.length}<p class="muted" id="blocks-hidden">
          Not offered for a {kind === "lxc" ? "container" : "virtual machine"}:
          {#each hidden as h, i (h)}{i ? ", " : ""}<span class="mono">{h}</span>{/each}.
        </p>{/if}
    {/if}

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
    {#if can("createPlan")}
      <div class="row">
        <button class="btn primary" type="submit" disabled={busy}>Show the plan</button>
        <a class="btn" href={href({ name: "targets" })}>Cancel</a>
      </div>
      <p class="muted">Nothing changes until you approve the plan.</p>
    {:else}
      <p class="muted" id="t-role">{roleNote("plan")} You can look, not change.</p>
    {/if}
  </form>
{:else if !loadError}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}
{#if loaded && existing}
  {#if existing.status.not_adopted}
    <div class="banner warn" role="alert" id="not-adopted">
      <strong>Not adopted: the guest changed.</strong> The adopt plan of this Target was approved,
      but its run did not bind the guest (it was renamed or changed in Proxmox before the approval).
      Plans do nothing for this Target. Either
      <a href={href({ name: "target-adopt" })}>adopt the guest again</a> (under its Proxmox name; the
      new plan removes this Target), or delete this Target below: that makes no Proxmox call and never
      touches the guest.
    </div>
  {/if}
  <RawResources {api} target={existing} />
  <AgentCard {api} target={existing} />
  {#if adopted}
    <p class="muted card" id="reenroll-adopted">
      An adopted Target is not re-enrolled: Plumb does not write into a guest that a person made.
      Use the install command on the <a
        href={href({ name: "target-agent", target: existing.document.name })}>agent page</a
      >.
    </p>
  {:else if can("reenrollTarget")}<form
      class="card"
      onsubmit={reenroll}
      aria-labelledby="reenroll-title"
    >
      <h2 id="reenroll-title">Re-enroll the agent</h2>
      <p>
        {forKind(
          "A new enrollment token becomes active at the next stop and start of the VM. Plumb plans it; nothing happens until you approve the plan.",
          kind,
        )}
      </p>
      {#if storedAllow}
        <p>
          {forKind(
            `The Run restarts the VM (or starts it when it is stopped). '${ALLOW_RESTART_LABEL}' stays on after it, until you switch it off.`,
            kind,
          )}
        </p>
      {:else if existing.status.power === "running"}
        <p class="warn" id="reenroll-refused">
          {forKind(
            `${REENROLL_REFUSED} (The VM ran when this page was loaded; the server checks again.)`,
            kind,
          )}
        </p>
      {:else}
        <p>{forKind("The VM is stopped: the Run starts it with the new token.", kind)}</p>
      {/if}
      <div class="field">
        <label for="reenroll-reason">Reason (optional)</label>
        <textarea id="reenroll-reason" bind:value={reenrollReason} maxlength="2000"></textarea>
      </div>
      <ProblemBox error={reenrollError} />
      <button
        class="btn"
        type="submit"
        disabled={reenrollBusy || restored.mode}
        aria-describedby={restored.mode ? "reenroll-restored" : undefined}
        >Plan the re-enrollment</button
      >
      {#if restored.mode}<p class="muted" id="reenroll-restored">{RESTORED_WAIT}</p>{/if}
    </form>{/if}
  <DeleteDocument {api} kind="Target" name={existing.document.name}>
    {#if existing.document.spec.adopted?.delete_guest}
      <p class="danger-text" id="delete-adopted-guest">
        <strong
          >The plan deletes the Proxmox guest
          <span class="mono">{existing.document.spec.adopted.vmid}</span> and its disks.</strong
        >
        You allowed this with delete_guest. This cannot be undone.
      </p>
    {:else if existing.document.spec.adopted}
      <p id="delete-adopted-keep">
        The plan lets the guest <span class="mono">{existing.document.spec.adopted.vmid}</span> go:
        Plumb removes its agent and repair key lines, and the Target leaves the desired state. The {kind ===
        "lxc"
          ? "container"
          : "VM"} and its disks stay in Proxmox.
      </p>
    {:else}
      <p id="delete-made">
        The plan deletes the {kind === "lxc" ? "container" : "VM"}
        {#if existing.status.guest_id}<span class="mono">{existing.status.guest_id}</span>{/if}
        and its disks in Proxmox, and the Target leaves the desired state. This cannot be undone.
      </p>
    {/if}
  </DeleteDocument>
  {#if existing.document.spec.adopted && !existing.document.spec.adopted.delete_guest && can("createPlan")}
    <section
      class="card danger-zone"
      aria-labelledby="allow-guest-delete-title"
      id="allow-guest-delete"
    >
      <h2 id="allow-guest-delete-title">Let Plumb delete the guest</h2>
      <p>
        By default, deleting this Target keeps the guest in Proxmox. This separate plan sets
        delete_guest: after it, deleting the Target <strong
          >deletes the Proxmox guest
          <span class="mono">{existing.document.spec.adopted.vmid}</span> and its disks</strong
        >. Only a person can approve it.
      </p>
      <ProblemBox error={allowError} />
      {#if confirmAllow}
        <div class="row">
          <button
            class="btn danger"
            type="button"
            id="allow-guest-delete-confirm"
            disabled={allowBusy}
            onclick={allowGuestDelete}>Make the plan</button
          >
          <button class="btn" type="button" onclick={() => (confirmAllow = false)}>Cancel</button>
        </div>
      {:else}
        <button class="btn danger" type="button" onclick={() => (confirmAllow = true)}
          >Allow Plumb to delete the guest…</button
        >
      {/if}
    </section>
  {/if}
{/if}

<style>
  .danger-text {
    color: var(--color-danger);
  }
  .danger-zone {
    border-left: 4px solid var(--color-danger);
  }
  .modes {
    display: flex;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .modes [aria-pressed="true"] {
    background: var(--color-accent);
    border-color: var(--color-accent);
    color: var(--color-accent-text);
  }
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
  .mismatch {
    border-left: 4px solid var(--color-warn);
  }
  .addr-card h2 {
    margin-top: 0;
  }
  .addrs {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--space-1) var(--space-3);
    margin: 0;
  }
  @media (max-width: 600px) {
    .addrs {
      grid-template-columns: minmax(0, 1fr);
      gap: 0;
    }
    .addrs dd {
      margin-bottom: var(--space-2);
    }
  }
  .addrs dd {
    margin: 0;
    overflow-wrap: anywhere;
  }
  .mark {
    font-weight: normal;
    font-style: italic;
    color: var(--color-text-muted);
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
