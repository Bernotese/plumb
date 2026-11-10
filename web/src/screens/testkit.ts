// Helpers for screen tests in happy-dom (D-112).
import { flushSync, mount, unmount } from "svelte";

let app: ReturnType<typeof mount> | undefined;

/** Mounts a screen into the body; `reset` removes it. */
export function show(component: unknown, props: Record<string, unknown>): void {
  const target = document.createElement("div");
  document.body.append(target);
  app = mount(component as Parameters<typeof mount>[0], { target, props: props as never });
}

export function reset(): void {
  if (app) unmount(app);
  app = undefined;
  document.body.innerHTML = "";
  location.hash = "";
}

export const text = (root: ParentNode = document.body): string =>
  (root as Element).textContent?.replace(/\s+/g, " ") ?? "";

export const rows = (): string[] =>
  [...document.querySelectorAll("tbody tr")].map(
    (r) => r.textContent?.replace(/\s+/g, " ").trim() ?? "",
  );

/** A button by its accessible name: aria-label, else its text. */
export const button = (label: string | RegExp): HTMLButtonElement | undefined =>
  [...document.querySelectorAll("button")].find((b) => {
    const t = b.getAttribute("aria-label") ?? b.textContent?.replace(/\s+/g, " ").trim() ?? "";
    return typeof label === "string" ? t === label : label.test(t);
  });

export function el<T extends Element = HTMLElement>(selector: string): T {
  const e = document.querySelector<T>(selector);
  if (!e) throw new Error(`no ${selector}`);
  return e;
}

/** Sets a field value and sends the event Svelte's binding listens to. */
export function type(selector: string, value: string): void {
  const e = el<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(selector);
  e.value = value;
  if (e instanceof HTMLSelectElement) {
    // happy-dom does not match ":checked" on options; Svelte's select
    // binding reads the value with it.
    const query = e.querySelector.bind(e);
    e.querySelector = ((q: string) =>
      q === ":checked" ? (e.options[e.selectedIndex] ?? null) : query(q)) as typeof e.querySelector;
  }
  e.dispatchEvent(
    new Event(e instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }),
  );
  flushSync();
}

export async function until<T>(fn: () => T | undefined | null | false, ms = 2000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    flushSync();
    const v = fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timeout; page: ${text()}`);
    await new Promise((r) => setTimeout(r, 10));
  }
}

/** Waits for a hash route "#/plans/N" and returns N. */
export const planOpened = async (): Promise<number> =>
  Number(await until(() => /^#\/plans\/(\d+)$/.exec(location.hash)?.[1]));
