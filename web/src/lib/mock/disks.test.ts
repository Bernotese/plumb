// T-239: the mock's data disk rules (internal/state/disks.go, ARCHITECTURE §21).
import { describe, expect, it } from "vitest";
import type { TargetDoc } from "../types";
import { checkDisks, planDisks, restoreDiskNotes } from "./disks";

type Spec = TargetDoc["spec"];
const spec = (type: "vm" | "lxc", disks?: Spec["guest"]["disks"], allow = false): Spec =>
  ({
    type,
    image: "x",
    guest: { cores: 1, memory: 512, disk: 8, bridge: "vmbr0", ...(disks ? { disks } : {}) },
    blocks: [],
    ...(allow ? { allow_restart: true } : {}),
  }) as Spec;

describe("checkDisks", () => {
  it("has a case for each refusal of disks.go (agent paths are server-only)", () => {
    expect(checkDisks("vm", [{ slot: 1, size_gb: 1, path: "/srv" }])[0]?.at).toBe("/0/path");
    expect(checkDisks("lxc", [{ slot: 1, size_gb: 1 }])[0]?.at).toBe("/0");
    expect(
      checkDisks("vm", [
        { slot: 2, size_gb: 1 },
        { slot: 2, size_gb: 1 },
      ])[0],
    ).toEqual({
      at: "/1/slot",
      message: "slot 2 is also used by disk 0; each slot names one disk",
    });
    const path = (p: string) => checkDisks("lxc", [{ slot: 1, size_gb: 1, path: p }])[0]?.message;
    expect(path("/")).toContain("absolute and not /");
    expect(path("/srv//x")).toContain("empty, . or .. part");
    expect(path("/srv/./x")).toContain("empty, . or .. part");
    // A top that starts with "lib" counts, as in disks.go (also /library).
    for (const p of ["/etc", "/var/lib/x", "/lib32", "/libx32/y", "/usr/local", "/library"])
      expect(path(p), p).toContain("system tree");
    for (const p of ["/srv/data", "/opt/x", "/data", "/mnt/a", "/home/u"])
      expect(path(p), p).toBeUndefined();
    const nested = checkDisks("lxc", [
      { slot: 1, size_gb: 1, path: "/srv/data" },
      { slot: 2, size_gb: 1, path: "/srv/data" },
      { slot: 3, size_gb: 1, path: "/srv" },
    ]);
    // As disks.go: one error per other disk that the path overlaps.
    expect(nested.map((e) => e.at)).toEqual(["/1/path", "/2/path", "/2/path"]);
  });
});

describe("planDisks", () => {
  const vm1 = [{ slot: 1, size_gb: 50 }];
  it("a missing field is not managed, also when it is removed", () => {
    expect(planDisks(spec("vm", vm1), spec("vm"))).toEqual({
      changes: [],
      warnings: [],
      errors: [],
    });
  });

  it("[] removes the observed disks (destructive, with the free step)", () => {
    const p = planDisks(spec("vm", vm1, true), spec("vm", [], true), "9002");
    expect(p.changes).toEqual([{ attr: "disk1", from: "50 GiB", risk: "destructive" }]);
    expect(p.warnings[0]).toContain("local-lvm:vm-9002-disk-1, 50 GiB) is lost");
    expect(p.warnings[0]).toContain("second step of the same Run");
  });

  it("a remove on a running guest needs allow_restart; a stopped one does not", () => {
    expect(planDisks(spec("vm", vm1), spec("vm", []), "1", "running").errors[0]?.message).toContain(
      "needs allow_restart",
    );
    expect(planDisks(spec("vm", vm1), spec("vm", []), "1", "stopped").errors).toEqual([]);
  });

  it("D-437: a running container is stopped for an add (allow_restart, disruptive)", () => {
    const ct = [{ slot: 1, size_gb: 10, path: "/srv/data" }];
    const no = planDisks(spec("lxc"), spec("lxc", ct), "9010", "running");
    expect(no.errors).toEqual([
      {
        at: "/0",
        message: "adding a container disk stops and starts the container: set allow_restart",
      },
    ]);
    const yes = planDisks(spec("lxc", undefined, true), spec("lxc", ct, true), "9010", "running");
    expect(yes.changes).toEqual([{ attr: "disk1", to: "10 GiB at /srv/data", risk: "disruptive" }]);
    // A stopped container stays stopped; a running VM gets the disk live.
    expect(planDisks(spec("lxc"), spec("lxc", ct), "9010", "stopped").changes[0]?.risk).toBe(
      "reversible",
    );
    const vm = planDisks(spec("vm"), spec("vm", [{ slot: 1, size_gb: 5 }]), "1", "running");
    expect(vm).toEqual({
      changes: [{ attr: "disk1", to: "5 GiB", risk: "reversible" }],
      warnings: [],
      errors: [],
    });
  });

  it("slot is the identity: a move to another slot is a remove and an add", () => {
    const p = planDisks(spec("vm", vm1, true), spec("vm", [{ slot: 2, size_gb: 50 }], true));
    expect(p.changes.map((c) => [c.attr, c.risk])).toEqual([
      ["disk1", "destructive"],
      ["disk2", "reversible"],
    ]);
  });

  it("refuses a shrink and a path change at the field", () => {
    const ct = [{ slot: 1, size_gb: 10, path: "/srv/data" }];
    const p = planDisks(spec("lxc", ct), spec("lxc", [{ slot: 1, size_gb: 5, path: "/srv/x" }]));
    expect(p.errors.map((e) => e.at)).toEqual(["/0/size_gb", "/0/path"]);
    expect(p.changes).toEqual([]);
  });
});

describe("restoreDiskNotes", () => {
  it("a VM keeps the volume as unused; an LXC loses the data", () => {
    const vm = restoreDiskNotes("vm", [{ slot: 2, size_gb: 20 }], "9002");
    expect(vm.warnings[0]).toContain("kept as an unused volume");
    expect(vm.warnings[0]).toContain("free the old volume by hand");
    const ct = restoreDiskNotes("lxc", [{ slot: 1, size_gb: 10, path: "/srv/data" }], "9010");
    expect(ct.warnings[0]).toContain("the data on disk 1 (local-lvm:vm-9010-disk-1) is lost");
    expect(ct.changes).toEqual([
      { attr: "disk1", from: "10 GiB at /srv/data", risk: "destructive" },
    ]);
  });
});
