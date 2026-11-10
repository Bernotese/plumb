// A snapshot that follows the event stream without gaps (T-014 review M2).
//
// The stream sends only events after it opened. A snapshot loaded before
// that can miss an event (for example run.finished). So the snapshot is
// loaded again every time the stream (re)opens. Events that arrive while a
// load runs are kept and applied to that snapshot. Reloads asked for by
// events are coalesced, and an older answer never replaces a newer one.
import type { PlumbEvent } from "./types";

export interface LiveOptions<T> {
  load: () => Promise<T>;
  /** Subscribes to the stream; must call onOpen on every (re)open. */
  subscribe: (onEvent: (e: PlumbEvent) => void, onOpen: () => void) => () => void;
  onSnapshot: (snapshot: T) => void;
  /** Applies an event to the current snapshot (only after a snapshot exists). */
  onEvent?: (e: PlumbEvent, live: Live) => void;
  onError?: (err: unknown) => void;
  /** Wait before an event-driven reload, in ms (default 300). */
  coalesceMs?: number;
}

export interface Live {
  /** Loads the snapshot again soon (coalesced). */
  reloadSoon(): void;
  stop(): void;
}

export function liveSnapshot<T>(o: LiveOptions<T>): Live {
  const wait = o.coalesceMs ?? 300;
  let generation = 0; // number of the newest load
  let shown = 0; // generation of the snapshot on screen
  let loading = 0; // loads in flight
  let buffer: PlumbEvent[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;

  const live: Live = {
    reloadSoon() {
      if (stopped || timer) return;
      timer = setTimeout(() => {
        timer = undefined;
        reload();
      }, wait);
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
      unsubscribe();
    },
  };

  function reload() {
    if (stopped) return;
    const gen = ++generation;
    loading++;
    o.load().then(
      (snapshot) => {
        loading--;
        if (!stopped && gen > shown) {
          shown = gen;
          o.onSnapshot(snapshot);
        } // else an older answer: dropped
        settle();
      },
      (err: unknown) => {
        loading--;
        if (!stopped) o.onError?.(err);
        settle();
      },
    );
  }

  // Every load end (shown, dropped or failed) checks the buffer: when no
  // load is in flight and a snapshot is shown, the buffered events are
  // applied to it in order (T-014 re-review m5).
  function settle() {
    if (stopped || loading > 0 || shown === 0) return;
    const queued = buffer;
    buffer = [];
    for (const e of queued) o.onEvent?.(e, live);
  }

  const unsubscribe = o.subscribe(
    (e) => {
      if (stopped) return;
      if (loading > 0 || shown === 0) buffer.push(e);
      else o.onEvent?.(e, live);
    },
    () => reload(), // every (re)open: the snapshot may have missed events
  );
  reload(); // show data at once, before the stream is open
  return live;
}
