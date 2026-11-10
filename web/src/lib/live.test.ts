import { describe, expect, it, vi } from "vitest";
import { liveSnapshot } from "./live";
import type { PlumbEvent } from "./types";

const ev = (seq: number, type: PlumbEvent["type"] = "run.step"): PlumbEvent => ({
  seq,
  type,
  time: "2026-11-10T00:00:00Z",
  subject: "run:1",
  actor: "system",
  data: {},
});

function stream() {
  let onEvent: (e: PlumbEvent) => void = () => {};
  let onOpen: () => void = () => {};
  return {
    subscribe: (e: (x: PlumbEvent) => void, o: () => void) => {
      onEvent = e;
      onOpen = o;
      return () => {};
    },
    emit: (e: PlumbEvent) => onEvent(e),
    open: () => onOpen(),
  };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe("liveSnapshot", () => {
  it("reloads when the stream opens, so a run that finished in the gap is seen", async () => {
    // The run finishes after the first snapshot and before the stream is open:
    // run.finished is never delivered. The reload on open sees it.
    const states = ["running", "succeeded"];
    const load = vi.fn(async () => states[Math.min(load.mock.calls.length - 1, 1)]);
    const s = stream();
    const shown: string[] = [];
    liveSnapshot({ load, subscribe: s.subscribe, onSnapshot: (x) => shown.push(x ?? "") });
    await tick();
    expect(shown).toEqual(["running"]);
    s.open();
    await tick();
    expect(shown).toEqual(["running", "succeeded"]);
    s.open(); // a reconnect reloads again
    await tick();
    expect(load).toHaveBeenCalledTimes(3);
  });

  it("applies events that arrive during a load after that snapshot", async () => {
    let resolve: (v: string) => void = () => {};
    const load = vi.fn(() => new Promise<string>((r) => (resolve = r)));
    const s = stream();
    const order: string[] = [];
    liveSnapshot({
      load,
      subscribe: s.subscribe,
      onSnapshot: (x) => order.push(`snap ${x}`),
      onEvent: (e) => order.push(`event ${e.seq}`),
    });
    s.emit(ev(1));
    s.emit(ev(2));
    resolve("a");
    await tick();
    s.emit(ev(3));
    expect(order).toEqual(["snap a", "event 1", "event 2", "event 3"]);
  });

  it("never lets an older answer replace a newer one", async () => {
    const resolvers: ((v: string) => void)[] = [];
    const load = vi.fn(() => new Promise<string>((r) => resolvers.push(r)));
    const s = stream();
    const shown: string[] = [];
    liveSnapshot({ load, subscribe: s.subscribe, onSnapshot: (x) => shown.push(x) });
    s.open(); // second load
    resolvers[1]?.("new");
    await tick();
    resolvers[0]?.("old");
    await tick();
    expect(shown).toEqual(["new"]);
  });

  it("coalesces event-driven reloads", async () => {
    vi.useFakeTimers();
    const load = vi.fn(async () => "x");
    const s = stream();
    const live = liveSnapshot({
      load,
      subscribe: s.subscribe,
      onSnapshot: () => {},
      coalesceMs: 300,
    });
    await vi.advanceTimersByTimeAsync(0);
    live.reloadSoon();
    live.reloadSoon();
    live.reloadSoon();
    await vi.advanceTimersByTimeAsync(300);
    expect(load).toHaveBeenCalledTimes(2);
    live.stop();
    live.reloadSoon();
    await vi.advanceTimersByTimeAsync(300);
    expect(load).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});
