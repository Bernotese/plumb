// @vitest-environment happy-dom
// T-177 (D-363): object and array-of-object Block parameters as nested
// forms in the Target form; key/value rows; a YAML field for the rest.
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import type { TargetDoc } from "../lib/types";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);

const params = {
  type: "object",
  required: ["site"],
  properties: {
    site: {
      type: "object",
      title: "Site",
      required: ["name"],
      properties: { name: { type: "string", pattern: "^[a-z]+$" }, tls: { type: "boolean" } },
    },
    upstreams: {
      type: "array",
      title: "Upstream",
      items: {
        type: "object",
        required: ["host"],
        properties: {
          host: { type: "string" },
          port: { type: "integer", minimum: 1, default: 80 },
        },
      },
    },
    labels: { type: "object", title: "Labels", additionalProperties: { type: "string" } },
    ports: { type: "array", title: "Ports", items: { type: "integer" } },
  },
};

async function setup() {
  const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
  const p = await api.createPlan({
    reason: "Nested block.",
    put: [
      {
        kind: "Block",
        name: "web-cfg",
        spec: {
          title: "Web config",
          params,
          resources: [{ file: { path: "/etc/web-cfg", content: "x\n" } }],
        },
      } as never,
    ],
  });
  await api.approvePlan(p.id);
  const createPlan = vi.spyOn(api, "createPlan");
  show(TargetForm, { api });
  await until(() => document.querySelector("#add-choice"));
  type("#add-choice", "web-cfg");
  button("Add Block")?.click();
  const site = await until(() => document.querySelector('[id$="-site-name"]'));
  return { api, createPlan, p: site.id.replace(/-site-name$/, "") };
}

const submit = () => {
  el<HTMLFormElement>("form").requestSubmit();
  flushSync();
};

describe("nested parameter forms (D-363)", () => {
  it("fills objects, items, rows and a YAML field, and saves typed parameters", async () => {
    const { createPlan, p } = await setup();
    expect(el(`#${p}-site-legend`).textContent).toContain("Site");
    type(`#${p}-site-name`, "shop");
    button("Add upstream")?.click();
    button("Add upstream")?.click();
    await until(() => document.querySelector(`#${p}-upstreams-1-host`));
    type(`#${p}-upstreams-0-host`, "a.lan");
    type(`#${p}-upstreams-1-host`, "b.lan");
    type(`#${p}-upstreams-1-port`, "8080");
    button("Move Upstream 2 up")?.click();
    flushSync();
    expect(el<HTMLInputElement>(`#${p}-upstreams-0-host`).value).toBe("b.lan");
    await until(() => document.activeElement?.id === `${p}-upstreams-0-legend`);
    button("Add a key")?.click();
    await until(() => document.querySelector(`#${p}-labels-0-name`));
    type(`#${p}-labels-0-name`, "env");
    type(`#${p}-labels-0-value`, "prod");
    type(`#${p}-ports`, "- 80\n- 443\n");
    type("#t-name", "shop1");
    type("#t-reason", "Shop.");
    submit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.blocks?.find((b) => b.use === "web-cfg")?.params).toEqual({
      site: { name: "shop", tls: false },
      upstreams: [
        { host: "b.lan", port: 8080 },
        { host: "a.lan", port: 80 },
      ],
      labels: { env: "prod" },
      ports: [80, 443],
    });
  });

  it("shows nested errors next to their fields and does not save", async () => {
    const { createPlan, p } = await setup();
    type(`#${p}-site-name`, "Shop");
    button("Add upstream")?.click();
    await until(() => document.querySelector(`#${p}-upstreams-0-host`));
    type(`#${p}-ports`, "- [");
    type("#t-name", "shop1");
    type("#t-reason", "Shop.");
    submit();
    expect(createPlan).not.toHaveBeenCalled();
    expect(el(`#${p}-site-name`).getAttribute("aria-invalid")).toBe("true");
    expect(el(`#${p}-site-name-error`).textContent).toBe("The value has the wrong format.");
    expect(el(`#${p}-upstreams-0-host-error`).textContent).toBe("This field is required.");
    expect(el(`#${p}-ports-error`).textContent).toMatch(/^YAML line 1: /);
  });

  it("removes an item and shows the empty state", async () => {
    const { p } = await setup();
    button("Add upstream")?.click();
    await until(() => document.querySelector(`#${p}-upstreams-0-host`));
    button("Remove Upstream 1")?.click();
    flushSync();
    expect(document.querySelector(`#${p}-upstreams-0-host`)).toBeNull();
    expect(text(el(`#${p}-upstreams`))).toContain("No entries.");
    await until(() => document.activeElement?.id === `${p}-upstreams-add`);
  });
});
