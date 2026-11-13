// Display rules of the Image screens (T-064). Pure functions, tested in images.test.ts.
import type { DiskUse, ImageSpec, ImageStatus, ImageVersion } from "./types";

export type Schedule = NonNullable<ImageSpec["schedule"]>;
export type Weekday = NonNullable<Schedule["weekday"]>;
export type Trigger = ImageVersion["trigger"];

export const WEEKDAYS: Weekday[] = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
const WEEKDAY_NAMES: Record<Weekday, string> = {
  mon: "Monday",
  tue: "Tuesday",
  wed: "Wednesday",
  thu: "Thursday",
  fri: "Friday",
  sat: "Saturday",
  sun: "Sunday",
};
export const weekdayName = (d: Weekday): string => WEEKDAY_NAMES[d];

/** "Every Sunday at 03:00 UTC", "Every day at 03:00 UTC", "On day 1 of every month at …". */
export function scheduleText(s: Schedule | undefined): string {
  if (!s) return "No schedule";
  switch (s.every) {
    case "day":
      return `Every day at ${s.at} UTC`;
    case "week":
      return `Every ${weekdayName(s.weekday ?? "sun")} at ${s.at} UTC`;
    case "month":
      return `On day ${s.day ?? 1} of every month at ${s.at} UTC`;
  }
}

/** The next slot of a schedule after `now`, in UTC (D-210). */
export function nextSlot(s: Schedule, now: Date): Date {
  const [h, m] = s.at.split(":").map(Number) as [number, number];
  const at = (y: number, mo: number, d: number) => new Date(Date.UTC(y, mo, d, h, m));
  const y = now.getUTCFullYear();
  const mo = now.getUTCMonth();
  const d = now.getUTCDate();
  if (s.every === "day") {
    const t = at(y, mo, d);
    return t > now ? t : at(y, mo, d + 1);
  }
  if (s.every === "week") {
    const want = (WEEKDAYS.indexOf(s.weekday ?? "sun") + 1) % 7; // Date: 0 = Sunday
    const ahead = (want - now.getUTCDay() + 7) % 7;
    const t = at(y, mo, d + ahead);
    return t > now ? t : at(y, mo, d + ahead + 7);
  }
  const t = at(y, mo, s.day ?? 1);
  return t > now ? t : at(y, mo + 1, s.day ?? 1);
}

export const TRIGGER_TEXT: Record<Trigger, string> = {
  change: "recipe change",
  manual: "Build now",
  schedule: "schedule",
};

/** The fixed steps of a build lane (D-199); the recipe resources come between them. */
export const BUILD_STEP_TEXT: Record<string, string> = {
  "image:base": "Base image: download and check the checksum",
  "guest:config": "Build guest: clone, disk check, first boot",
  "image:seal": "Seal: clean the guest for a template, then power off",
  "image:template": "Make the template",
  "image:retention": "Remove old versions (retention)",
};

/** The newest version with the state `ready`, or null. */
export function newestReady(st: ImageStatus | undefined): ImageVersion | null {
  return (
    st?.versions.filter((v) => v.status === "ready").sort((a, b) => b.version - a.version)[0] ??
    null
  );
}

/**
 * "1.4 GiB", "612 MiB", "190 KiB", "512 B". The unit is chosen after the
 * rounding, so no size shows as "0 MiB" or "1024 KiB".
 */
export function bytesText(n: number): string {
  if (n < 2 ** 10) return `${Math.round(n)} B`;
  const kib = Math.round(n / 2 ** 10);
  if (kib < 2 ** 10) return `${kib} KiB`;
  const mib = Math.round(n / 2 ** 20);
  if (mib < 2 ** 10) return `${mib} MiB`;
  return `${(n / 2 ** 30).toFixed(1)} GiB`;
}

/**
 * The use of the disk storage now and with one more build disk (D-203):
 * `after` counts the builds that run now (`reserved`) and the new disk.
 */
export function diskUse(
  d: DiskUse,
  buildDiskGb = 0,
): { now: number; after: number; over: boolean; text: string } {
  const pct = (b: number) => (d.total_bytes > 0 ? Math.round((b / d.total_bytes) * 100) : 0);
  const now = pct(d.used_bytes + d.reserved_bytes);
  const need = d.used_bytes + d.reserved_bytes + buildDiskGb * 2 ** 30;
  const over = overLimit(need, d.total_bytes, d.limit_percent);
  // Rounded only for display. A refused build never shows the limit itself.
  let after = pct(need);
  if (over && d.total_bytes > 0 && after <= d.limit_percent) after = d.limit_percent + 1;
  const text = `${d.storage}: ${bytesText(d.used_bytes)} of ${bytesText(d.total_bytes)} used (${pct(d.used_bytes)} %)${
    d.reserved_bytes ? `, ${bytesText(d.reserved_bytes)} held by running builds of all images` : ""
  }.`;
  return { now, after, over, text };
}

/**
 * The exact 85 % rule of D-203 (QA of PR #123): a build is refused when
 * `need × 100 > limit × total`, in integer bytes with no rounding. The
 * server (T-062) uses the same comparison. BigInt: byte counts × 100 can
 * pass 2^53.
 */
export function overLimit(needBytes: number, totalBytes: number, limitPercent: number): boolean {
  const toBig = (n: number) => BigInt(Math.trunc(n));
  return toBig(needBytes) * 100n > toBig(limitPercent) * toBig(totalBytes);
}

/** The reason of answer 7 (D-203) for a build that would fill the storage. */
export function diskLimitText(d: DiskUse, buildDiskGb: number): string {
  const u = diskUse(d, buildDiskGb);
  return `A build does not start when ${d.storage} would be more than ${d.limit_percent} % full with the whole build disk. With this build: ${u.after} %.`;
}

/** The smallest disk of a Target from this Image (D-202), or null for a stock Image. */
export function smallestDisk(spec: ImageSpec | undefined): number | null {
  return spec?.build?.guest.disk ?? null;
}

/**
 * A root command for an unused import file is offered only in Plumb's own
 * form (review S1 of PR #123): `pvesm free <storage>:import/plumb-<name>-<12 hex>.qcow2`.
 * Anything else (a file someone put into the import dir) gets no command.
 */
const PVESM_FREE =
  /^pvesm free [A-Za-z][A-Za-z0-9._-]*:(import\/plumb-[a-z0-9][a-z0-9.-]*-[0-9a-f]{12}\.qcow2|vztmpl\/plumb-ct-[a-z0-9][a-z0-9.-]*-[0-9a-f]{12}\.tar\.gz)$/;

export function safeFreeCommand(command: string | null | undefined): string | null {
  return typeof command === "string" && PVESM_FREE.test(command) ? command : null;
}

/**
 * Proxmox system templates with the pin from the signed Proxmox appliance
 * index (docs/ARCHITECTURE.md, the table of `lxc` Images; a test keeps both
 * equal). The form fills url and sha512 from it (T-038 §8).
 */
export const KNOWN_LXC_TEMPLATES: readonly { label: string; url: string; sha512: string }[] = [
  {
    label: "Debian 13",
    url: "http://download.proxmox.com/images/system/debian-13-standard_13.6-1_amd64.tar.zst",
    sha512:
      "4c0c27ca6ceab5ef0b84db57825a00f26157ef1854bafe97297813e1cbe8ecb8cc9c453cab6b3b0efe1ba193a50c47ece1e41d950e411b8730b835b71e9e754b",
  },
  {
    label: "Ubuntu 26.04",
    url: "http://download.proxmox.com/images/system/ubuntu-26.04-standard_26.04-1_amd64.tar.zst",
    sha512:
      "d5607f124d01f8ddfebd8e7da34c0022fb2a464e1662ee1e39a13f5ed1bae08a7364c486247fbd9f69f26c710729884ee5047aadb89f33a40066cd5f0d9def88",
  },
];

/**
 * The base pin of a version (D-254): the SHA-256 of a vm base (64 hex) or the
 * SHA-512 pin of an lxc base (128 hex). `short` is its first 12 digits; null
 * for any other value.
 */
export function basePin(
  pin: string | null | undefined,
): { algo: "sha256" | "sha512"; short: string } | null {
  if (typeof pin !== "string") return null;
  if (/^[0-9a-f]{64}$/u.test(pin)) return { algo: "sha256", short: pin.slice(0, 12) };
  if (/^[0-9a-f]{128}$/u.test(pin)) return { algo: "sha512", short: pin.slice(0, 12) };
  return null;
}
