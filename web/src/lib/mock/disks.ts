// Data disks in the mock (T-239, ARCHITECTURE §21, D-423, D-436): the
// checks of internal/state/disks.go (without the agent paths, which only
// the server knows) and the Plan lines of the risk table. The mock has no
// guest config, so the observed disks are the disks of the stored document.
import { isSystemTree } from "../disks";
import type { Change, Risk, TargetDoc } from "../types";

export type Disk = NonNullable<TargetDoc["spec"]["guest"]["disks"]>[number];

/** The disk storage of the mock install. */
export const DISK_STORAGE = "local-lvm";
export const MAX_DISKS = 3;

/** Why `p` cannot be the mount path of a container disk, or "" (diskPathProblem). */
export function diskPathProblem(p: string): string {
  if (!p.startsWith("/") || p === "/") return "the path must be absolute and not /";
  const parts = p.slice(1).split("/");
  if (parts.some((x) => x === "" || x === "." || x === ".."))
    return `the path ${p} has an empty, . or .. part`;
  const top = parts[0] ?? "";
  if (isSystemTree(top))
    return `the path ${p} is in the system tree /${top}; use for example /srv, /opt, /data, /mnt or /home`;
  return "";
}

const within = (p: string, q: string) => p === q || p.startsWith(`${q}/`);

export interface DiskError {
  /** Below `/spec/guest/disks`: `/0`, `/0/slot`, `/1/path`. */
  at: string;
  message: string;
}

/** The checks of checkDisks (internal/state/disks.go) for one Target. */
export function checkDisks(type: string, disks: readonly Disk[]): DiskError[] {
  const errs: DiskError[] = [];
  const slots = new Map<number, number>();
  const paths: { i: number; p: string }[] = [];
  disks.forEach((d, i) => {
    const before = slots.get(d.slot);
    if (before !== undefined)
      errs.push({
        at: `/${i}/slot`,
        message: `slot ${d.slot} is also used by disk ${before}; each slot names one disk`,
      });
    else slots.set(d.slot, i);
    const has = d.path !== undefined;
    if (type === "vm" && has) {
      errs.push({
        at: `/${i}/path`,
        message:
          "a VM data disk has no path: it is a raw block device (serial plumb-<slot>); format and mount it in the guest",
      });
      return;
    }
    if (type === "lxc" && !has) {
      errs.push({
        at: `/${i}`,
        message: "a container data disk needs a path, for example /srv/data",
      });
      return;
    }
    if (!has) return;
    const p = d.path ?? "";
    const why = diskPathProblem(p);
    if (why) {
      errs.push({ at: `/${i}/path`, message: why });
      return;
    }
    for (const q of paths)
      if (within(p, q.p) || within(q.p, p))
        errs.push({
          at: `/${i}/path`,
          message: `${p} is at or inside the path ${q.p} of disk ${q.i}`,
        });
    paths.push({ i, p });
  });
  return errs;
}

/** The Plan value of a disk: "20 GiB", a container disk "20 GiB at /srv/data". */
export const diskValue = (d: Disk) =>
  d.path !== undefined ? `${d.size_gb} GiB at ${d.path}` : `${d.size_gb} GiB`;

/** The volume of a Plumb disk (`<storage>:vm-<vmid>-disk-<slot>`). */
export const diskVolume = (vmid: string, slot: number) => `${DISK_STORAGE}:vm-${vmid}-disk-${slot}`;

export interface DiskPlan {
  changes: Change[];
  warnings: string[];
  errors: DiskError[];
}

/**
 * The disk lines of `guest:config` (ARCHITECTURE §21 risk table). A missing
 * `disks` is not managed: no line, also when the field is removed (it
 * deletes nothing). `[]` or a list manages slots 1 … 3 against the
 * observed disks.
 */
export function planDisks(
  old: TargetDoc["spec"] | undefined,
  next: TargetDoc["spec"],
  vmid = "9000",
  power = "running",
): DiskPlan {
  const want = next.guest.disks;
  const out: DiskPlan = { changes: [], warnings: [], errors: [] };
  if (want === undefined) return out;
  out.errors = checkDisks(next.type, want);
  if (out.errors.length) return out;
  const seen = old?.guest.disks ?? [];
  const removes: Disk[] = [];
  for (let slot = 1; slot <= MAX_DISKS; slot++) {
    const o = seen.find((d) => d.slot === slot);
    const i = want.findIndex((d) => d.slot === slot);
    const n = want[i];
    const attr = `disk${slot}`;
    if (!o && n) {
      // #365 (ARCHITECTURE §21): a stopped container is started for the add
      // and stopped again; a running one and a VM get the disk live.
      const cold = next.type === "lxc" && power === "stopped";
      if (cold && next.allow_restart !== true)
        out.errors.push({
          at: `/${i}`,
          message: "the container is stopped: start it first, or set allow_restart",
        });
      out.changes.push({ attr, to: diskValue(n), risk: cold ? "disruptive" : "reversible" });
    } else if (o && n) {
      if (n.size_gb < o.size_gb)
        out.errors.push({
          at: `/${i}/size_gb`,
          message: `disk ${slot} can only grow: ${o.size_gb} GiB now, ${n.size_gb} GiB asked.`,
        });
      if ((n.path ?? "") !== (o.path ?? ""))
        out.errors.push({
          at: `/${i}/path`,
          message: `the path of disk ${slot} is create-only (${o.path ?? "none"} now); move the data in the guest instead.`,
        });
      if (n.size_gb > o.size_gb)
        out.changes.push({ attr, from: diskValue(o), to: diskValue(n), risk: "disruptive" });
    } else if (o && !n) {
      removes.push(o);
      out.changes.push({ attr, from: diskValue(o), risk: "destructive" });
      out.warnings.push(
        `${next.type === "vm" ? "VM" : "Container"} disk ${slot}: the data on disk ${slot} (${diskVolume(vmid, slot)}, ${o.size_gb} GiB) is lost. Plumb detaches the disk, then frees its volume in a second step of the same Run.`,
      );
    }
  }
  if (removes.length && power === "running" && next.allow_restart !== true)
    out.errors.push({
      at: "",
      message:
        "removing a data disk of a running guest needs allow_restart (a container applies it at the restart).",
    });
  return out;
}

/**
 * Restore of a backup older than a disk (F3 of #359, T-244 (1)): the
 * warning for each observed disk that the backup lacks.
 */
export function restoreDiskNotes(
  type: string,
  lacking: readonly Disk[],
  vmid: string,
): { changes: Change[]; warnings: string[] } {
  const changes: Change[] = [];
  const warnings: string[] = [];
  for (const d of lacking) {
    const vol = diskVolume(vmid, d.slot);
    if (type === "vm") {
      changes.push({
        attr: `disk${d.slot}`,
        from: diskValue(d),
        to: "kept as an unused volume",
        risk: "destructive" as Risk,
      });
      warnings.push(
        `The backup has no disk ${d.slot} (added later): disk ${d.slot} (${vol}) is detached and kept as an unused volume; the next Run attaches a new empty disk in slot ${d.slot}; free the old volume by hand. The safety backup holds it.`,
      );
    } else {
      changes.push({ attr: `disk${d.slot}`, from: diskValue(d), risk: "destructive" as Risk });
      warnings.push(
        `The backup has no disk ${d.slot} (added later): the data on disk ${d.slot} (${vol}) is lost; the next Run makes a new empty disk at ${d.path ?? "its path"}. The safety backup holds it.`,
      );
    }
  }
  return { changes, warnings };
}
