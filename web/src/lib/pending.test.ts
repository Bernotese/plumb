// T-056: the display rules of pending values, allow_restart and the state
// "restart needed" (D-190, D-194, D-195).
import { describe, expect, it } from "vitest";
import type { Change, TargetView } from "./types";
import {
  ageText,
  changeNotes,
  changeView,
  RESTART_TEXT,
  targetState,
  waitingLines,
  waitingSince,
} from "./view";

const G = "guest:config";

describe("changeNotes", () => {
  it("explains a pending value from outside", () => {
    const c = { attr: "memory", from: 256, to: 1024, risk: "reversible", pending: true } as Change;
    const n = changeNotes(c, G);
    expect(n.tags).toEqual(["pending"]);
    expect(n.notes).toEqual([
      "Set in Proxmox but not active. The VM uses 1024. 256 becomes active at the next stop and start of the VM. A reboot inside the guest is not enough.",
    ]);
  });
  it("tells how to remove a pending create-only input", () => {
    const c = {
      attr: "ip",
      from: "192.0.2.9/24",
      to: "dhcp",
      risk: "destructive",
      pending: true,
    } as Change;
    expect(changeNotes(c, G).notes[1]).toContain("qm set <vmid> --revert <key>");
  });
  it("names the also-pending value", () => {
    const c = { attr: "cores", from: 8, to: 2, risk: "disruptive", also_pending: 4 } as Change;
    expect(changeNotes(c, G)).toEqual({ tags: [], notes: ["Also pending: 4."] });
  });
  it("marks a write that waits, and a value that waits next to a restart", () => {
    const w = {
      attr: "memory",
      from: 1024,
      to: 2048,
      risk: "disruptive",
      waits_for_restart: true,
    } as Change;
    const n = changeNotes(w, G);
    expect(n.tags).toEqual(["after restart"]);
    expect(n.notes[0]).toContain(
      "Plumb writes 2048 to Proxmox. The VM keeps 1024 until its next stop and start.",
    );
    expect(n.notes[0]).toContain("(allow_restart is off)");
    const r = changeNotes({ ...w, risk: "read" } as Change, G);
    expect(r.notes[0]).toBe(
      "2048 is set in Proxmox already and waits. The VM uses 1024 until the restart of this Plan.",
    );
  });
  it("gives the restart of guest:config its text, not a service restart", () => {
    const c = { attr: "restart", risk: "disruptive" } as Change;
    expect(changeNotes(c, G)).toEqual({ tags: ["restart"], notes: [RESTART_TEXT] });
    expect(RESTART_TEXT).toContain("hard power-off after 2 minutes");
    expect(changeNotes(c, "service:nginx")).toEqual({ tags: [], notes: [] });
  });
  it("shows allow_restart with its label and yes/no", () => {
    const c = { attr: "allow_restart", from: false, to: true, risk: "read" } as Change;
    expect(changeView(c, G)).toMatchObject({
      attr: "Plumb may restart this VM",
      from: "no",
      to: "yes",
    });
    expect(changeNotes(c, G).notes).toEqual(["A rule of Plumb: nothing changes in Proxmox."]);
  });
  it("says nothing for other resources and for sensitive changes", () => {
    const c = { attr: "memory", from: 1, to: 2, risk: "disruptive", pending: true } as Change;
    expect(changeNotes(c, "file:/x")).toEqual({ tags: [], notes: [] });
    expect(changeNotes({ attr: "enroll_token", sensitive: true, risk: "disruptive" }, G)).toEqual({
      tags: [],
      notes: [],
    });
  });
});

describe("restart needed", () => {
  const waiting: TargetView["status"]["waiting"] = [
    { attr: "memory", from: 2048, to: 4096, since: "2026-11-10T09:00:00Z" },
    { attr: "cores", from: 1, to: 2, since: "2026-11-10T08:00:00Z" },
  ];
  it("lists each value with the running one first", () => {
    expect(waitingLines(waiting)).toEqual([
      "memory: the VM uses 2048 MiB. 4096 MiB is set and becomes active at the next stop and start of the VM.",
      "cores: the VM uses 1. 2 is set and becomes active at the next stop and start of the VM.",
    ]);
    expect(waitingSince(waiting)).toBe("2026-11-10T08:00:00Z");
    expect(waitingSince([])).toBeNull();
  });
  it("tells the age short", () => {
    const now = new Date("2026-11-10T10:00:00Z");
    expect(ageText("2026-11-10T09:59:30Z", now)).toBe("under 1 min");
    expect(ageText("2026-11-10T09:15:00Z", now)).toBe("45 min");
    expect(ageText("2026-11-10T07:00:00Z", now)).toBe("3 h");
    expect(ageText("2026-11-07T10:00:00Z", now)).toBe("3 days");
  });
  it("has a state label", () => {
    const t = {
      status: {
        phase: "restart_needed",
        agent: "online",
        power: "running",
        open_issues: 0,
        waiting,
      },
    } as unknown as TargetView;
    expect(targetState(t)).toEqual({
      label: "Restart needed",
      tone: "warn",
      detail: "2 values set in Proxmox, waiting for a stop and start of the VM.",
    });
  });
});
