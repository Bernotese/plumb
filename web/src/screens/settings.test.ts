// @vitest-environment happy-dom
// Screen tests of T-035 on the mock: tokens, secrets, export and import.
import { flushSync, mount, unmount } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { PlumbApi } from "../lib/client";
import type { Session } from "../lib/types";
import ExportImport from "./ExportImport.svelte";
import Secrets from "./Secrets.svelte";
import Settings from "./Settings.svelte";
import Tokens from "./Tokens.svelte";

let app: ReturnType<typeof mount> | undefined;
afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  document.body.innerHTML = "";
  location.hash = "";
});

async function until<T>(fn: () => T | undefined | null | false, ms = 2000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    flushSync();
    const v = fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timeout; page: ${text()}`);
    await new Promise((r) => setTimeout(r, 10));
  }
}

const text = () => document.body.textContent?.replace(/\s+/g, " ") ?? "";
const rows = () =>
  [...document.querySelectorAll("tbody tr")].map(
    (r) => r.textContent?.replace(/\s+/g, " ").trim() ?? "",
  );
const button = (label: string | RegExp) =>
  [...document.querySelectorAll("button")].find((b) => {
    const t = b.getAttribute("aria-label") ?? b.textContent?.replace(/\s+/g, " ").trim() ?? "";
    return typeof label === "string" ? t === label : label.test(t);
  });
function el<T extends Element>(selector: string): T {
  const e = document.querySelector<T>(selector);
  if (!e) throw new Error(`no ${selector}`);
  return e;
}
function type(selector: string, value: string) {
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
function show(component: unknown, props: Record<string, unknown>) {
  const target = document.createElement("div");
  document.body.append(target);
  app = mount(component as typeof Tokens, { target, props: props as never });
}
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

describe("Settings", () => {
  it("shows the tabs to a person", async () => {
    const api = adminApi();
    show(Settings, { api, session: await api.session(), tab: "secrets" });
    const current = el<HTMLAnchorElement>('nav[aria-label="Settings"] a[aria-current="page"]');
    expect(current.textContent).toBe("Secrets");
    expect(current.getAttribute("href")).toBe("#/settings/secrets");
  });

  it("keeps a token session out", async () => {
    const api = createMockApi({ token: true });
    const session: Session = await api.session();
    show(Settings, { api, session, tab: "tokens" });
    expect(el('[role="alert"]').textContent).toContain(
      "Only a person who logged in can open the settings.",
    );
    expect(document.querySelector('nav[aria-label="Settings"]')).toBeNull();
    expect(document.querySelector("form")).toBeNull();
  });

  it("shows the problem text when the API answers 403", async () => {
    const api = createMockApi({ token: true });
    show(Tokens, { api });
    const alert = await until(() => document.querySelector('[role="alert"]'));
    expect(alert.textContent).toContain("Human session required.");
    expect(alert.textContent).toContain("Only a person who logged in can manage tokens");
  });
});

describe("Tokens", () => {
  it("lists tokens with tier, max risk, dates and status", async () => {
    show(Tokens, { api: adminApi() });
    await until(() => rows().length === 2);
    expect(rows()[0]).toContain("Claude (plans and comments)");
    expect(rows()[0]).toContain("Active");
    expect(rows()[1]).toContain("CI deploy");
    expect(rows()[1]).toContain("reversible");
    expect(rows()[1]).toContain("Never");
    expect(rows()[1]).toContain("Revoked");
    expect(text()).toContain("An apply token is root on the guests.");
  });

  it("asks max risk only for an apply token", async () => {
    show(Tokens, { api: adminApi() });
    await until(() => document.querySelector("#token-tier"));
    expect(document.querySelector("#token-risk")).toBeNull();
    type("#token-tier", "apply");
    expect(document.querySelector("#token-risk")).not.toBeNull();
    type("#token-tier", "read");
    expect(document.querySelector("#token-risk")).toBeNull();
  });

  it("shows the bearer once, copies it and forgets it", async () => {
    const api = adminApi();
    const create = vi.spyOn(api, "createToken");
    const clip = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: clip },
      configurable: true,
    });
    show(Tokens, { api });
    await until(() => rows().length === 2);
    type("#token-name", "Deploy bot");
    type("#token-tier", "apply");
    type("#token-risk", "disruptive");
    button("Create token")?.click();
    const value = await until(() => document.querySelector("#bearer-value"));
    expect(create).toHaveBeenCalledWith({
      name: "Deploy bot",
      tier: "apply",
      max_risk: "disruptive",
    });
    const bearer = value.textContent ?? "";
    expect(bearer).toMatch(/^tok_deploy_bot\.[0-9a-f]{32}$/);
    expect(text()).toContain("You cannot see it again.");
    expect(location.href).not.toContain(bearer.split(".")[1]);
    button("Copy token")?.click();
    await until(() => text().includes("Copied to the clipboard."));
    expect(clip).toHaveBeenCalledWith(bearer);
    await until(() => rows().length === 3);
    expect(rows()[0]).toContain("Deploy bot");
    button("Done, I copied it")?.click();
    flushSync();
    expect(document.querySelector("#bearer-value")).toBeNull();
    expect(document.body.innerHTML).not.toContain(bearer);
  });

  it("revokes only after a confirmation", async () => {
    const api = adminApi();
    const revoke = vi.spyOn(api, "revokeToken");
    show(Tokens, { api });
    await until(() => rows().length === 2);
    button("Revoke Claude (plans and comments)")?.click();
    const confirm = await until(() => button("Revoke token"));
    await until(() => document.activeElement === confirm);
    button("Cancel")?.click();
    flushSync();
    expect(revoke).not.toHaveBeenCalled();
    button("Revoke Claude (plans and comments)")?.click();
    (await until(() => button("Revoke token"))).click();
    await until(() => rows()[0]?.includes("Revoked"));
    expect(revoke).toHaveBeenCalledWith("tok_claude");
  });
});

describe("Secrets", () => {
  it("lists secrets and blocks delete of a used one with a reason", async () => {
    show(Secrets, { api: adminApi() });
    await until(() => rows().length === 2);
    expect(rows()[0]).toContain("app-db");
    expect(rows()[0]).toContain("web1");
    const del = button("Delete app-db");
    expect(del?.disabled).toBe(true);
    const why = el(`#${del?.getAttribute("aria-describedby") ?? "x"}`);
    expect(why.textContent?.replace(/\s+/g, " ")).toContain(
      "In use by 1 target. Remove the references first.",
    );
    expect(button("Delete deploy-key")?.disabled).toBe(false);
  });

  it("deletes an unused secret after a confirmation", async () => {
    const api = adminApi();
    show(Secrets, { api });
    await until(() => rows().length === 2);
    button("Delete deploy-key")?.click();
    (await until(() => button("Delete secret"))).click();
    await until(() => rows().length === 1);
    expect((await api.listSecrets()).map((s) => s.name)).toEqual(["app-db"]);
  });

  it("sets a new secret with a password field and clears the value", async () => {
    const api = adminApi();
    const put = vi.spyOn(api, "putSecret");
    show(Secrets, { api });
    await until(() => rows().length === 2);
    const value = el<HTMLInputElement>("#secret-value");
    expect(value.type).toBe("password");
    expect(value.getAttribute("autocomplete")).toBe("off");
    type("#secret-name", "smtp");
    type("#secret-value", "s3cret value");
    button("Save secret")?.click();
    await until(() => rows().length === 3);
    expect(put).toHaveBeenCalledWith("smtp", "s3cret value");
    expect(el<HTMLInputElement>("#secret-value").value).toBe("");
    expect(document.body.innerHTML).not.toContain("s3cret value");
    // No plan offer: nobody uses the new secret.
    expect(button(/^Plan the change/)).toBeUndefined();
  });

  it("switches to a multi-line field for keys", async () => {
    show(Secrets, { api: adminApi() });
    await until(() => rows().length === 2);
    const box = el<HTMLInputElement>('input[type="checkbox"]');
    box.click();
    flushSync();
    const area = el<HTMLTextAreaElement>("textarea#secret-value");
    expect(area.getAttribute("autocomplete")).toBe("off");
    expect(area.getAttribute("spellcheck")).toBe("false");
  });

  it("after a replace of a used secret offers to plan its targets", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(Secrets, { api });
    await until(() => rows().length === 2);
    button("Replace app-db")?.click();
    await until(() => document.activeElement?.id === "secret-value");
    expect(el<HTMLInputElement>("#secret-name").readOnly).toBe(true);
    type("#secret-value", "new db password");
    button("Replace value")?.click();
    const plan = await until(() => button("Plan the change for 1 target"));
    expect(text()).toContain("The guests get it only through a plan.");
    plan.click();
    const id = await until(() => /^#\/plans\/(\d+)$/.exec(location.hash)?.[1]);
    expect(createPlan).toHaveBeenCalledWith({
      reason: "Send the new value of secret app-db.",
      targets: ["web1"],
    });
    const p = await api.getPlan(Number(id));
    expect(p.targets.map((t) => t.target)).toEqual(["web1"]);
  });
});

describe("Export and import", () => {
  function pickFile(content: string, name = "plumb-export.yaml") {
    const input = el<HTMLInputElement>("#import-file");
    Object.defineProperty(input, "files", {
      value: [new File([content], name, { type: "application/yaml" })],
      configurable: true,
    });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  }

  it("downloads an export without secret values", async () => {
    const save = vi.fn();
    show(ExportImport, { api: adminApi(), save });
    button("Download export")?.click();
    await until(() => save.mock.calls.length === 1);
    const [file, body] = save.mock.calls[0] as [string, string];
    expect(file).toMatch(/^plumb-export-\d{4}-\d\d-\d\d\.yaml$/);
    expect(body).toMatch(/^kind: Export\nformat: 1\n/);
    expect(body).toContain("kind: Target");
    expect(body).not.toContain("SecretBundle");
    await until(() => document.querySelector('[role="status"]'));
  });

  it("asks a passphrase twice, at least 12 characters, for an export with secrets", async () => {
    const api = adminApi();
    const exp = vi.spyOn(api, "exportState");
    const save = vi.fn();
    show(ExportImport, { api, save });
    el<HTMLInputElement>('input[name="export-secrets"][value="true"]').click();
    flushSync();
    const submit = button("Download export");
    expect(submit?.disabled).toBe(true);
    type("#export-pass", "too short");
    expect(el("#export-pass").getAttribute("aria-invalid")).toBe("true");
    type("#export-pass", "long enough passphrase");
    type("#export-pass2", "long enough passphrasX");
    expect(text()).toContain("The two passphrases differ.");
    expect(submit?.disabled).toBe(true);
    type("#export-pass2", "long enough passphrase");
    expect(submit?.disabled).toBe(false);
    submit?.click();
    await until(() => save.mock.calls.length === 1);
    expect(exp).toHaveBeenCalledWith("long enough passphrase");
    const [file, body] = save.mock.calls[0] as [string, string];
    expect(file).toContain("-with-secrets.yaml");
    expect(body).toContain("kind: SecretBundle");
    expect(el<HTMLInputElement>("#export-pass").value).toBe("");
  });

  it("imports a file in merge mode and opens the plan", async () => {
    const api = adminApi();
    const imp = vi.spyOn(api, "importState");
    const file = await api.exportState();
    show(ExportImport, { api, save: vi.fn() });
    pickFile(file);
    await until(() => text().includes("Selected: plumb-export.yaml"));
    el<HTMLInputElement>('input[name="import-mode"][value="merge"]').click();
    type("#import-reason", "Restore.");
    button("Make import plan")?.click();
    const id = await until(() => /^#\/plans\/(\d+)$/.exec(location.hash)?.[1]);
    expect(imp).toHaveBeenCalledWith({ content: file, mode: "merge", reason: "Restore." });
    expect((await api.getPlan(Number(id))).origin).toBe("import");
  });

  it("on secret-conflict lists the names unticked and sends the ticked ones", async () => {
    const api = adminApi();
    const imp = vi.spyOn(api, "importState");
    const file = await api.exportState("long enough passphrase");
    await api.putSecret("app-db", "changed since the export");
    await api.putSecret("deploy-key", "changed too");
    show(ExportImport, { api, save: vi.fn() });
    pickFile(file);
    await until(() => text().includes("Selected:"));
    type("#import-pass", "long enough passphrase");
    type("#import-reason", "Restore with secrets.");
    button("Make import plan")?.click();
    await until(() => text().includes("Secrets with another value"));
    const boxes = [...document.querySelectorAll<HTMLInputElement>(".conflicts input")];
    expect(boxes.map((b) => [b.value, b.checked])).toEqual([
      ["app-db", false],
      ["deploy-key", false],
    ]);
    boxes[0]?.click();
    flushSync();
    button("Import again")?.click();
    // deploy-key is still a conflict: the list stays, app-db stays ticked.
    await until(() => imp.mock.calls.length === 2);
    await until(() => !button("Importing…"));
    expect(imp.mock.calls[1]?.[0].overwrite).toEqual(["app-db"]);
    expect(
      [...document.querySelectorAll<HTMLInputElement>(".conflicts input")].map((b) => b.checked),
    ).toEqual([true, false]);
    document.querySelectorAll<HTMLInputElement>(".conflicts input")[1]?.click();
    flushSync();
    button("Import again")?.click();
    const id = await until(() => /^#\/plans\/(\d+)$/.exec(location.hash)?.[1]);
    expect(imp.mock.calls[2]?.[0].overwrite).toEqual(["app-db", "deploy-key"]);
    const plan = await api.getPlan(Number(id));
    expect(plan.warnings.join(" ")).toContain("Secret app-db was overwritten");
  });

  it("lists missing secrets with a link to the secrets page", async () => {
    const api = adminApi();
    show(ExportImport, { api, save: vi.fn() });
    pickFile(
      "kind: Export\nformat: 1\nsecrets: [nope]\n---\nkind: Target\nname: x\nspec:\n  p: {secret: nope}\n",
    );
    await until(() => text().includes("Selected:"));
    type("#import-reason", "Try.");
    button("Make import plan")?.click();
    const alert = await until(() => document.querySelector('[role="alert"]'));
    expect(alert.textContent).toContain("The file refers to secrets that do not exist:");
    expect(alert.textContent).toContain("nope");
    expect(alert.querySelector("a")?.getAttribute("href")).toBe("#/settings/secrets");
  });

  it("refuses a file above 8 MiB before sending it", async () => {
    const api: PlumbApi = adminApi();
    const imp = vi.spyOn(api, "importState");
    show(ExportImport, { api, save: vi.fn() });
    pickFile("x".repeat(8 * 1024 * 1024 + 1));
    await until(() => text().includes("larger than 8 MiB"));
    expect(button("Make import plan")?.disabled).toBe(true);
    expect(imp).not.toHaveBeenCalled();
  });
});
