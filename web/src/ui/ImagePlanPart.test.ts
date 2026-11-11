// @vitest-environment happy-dom
// The disk notes of a build Plan are shown to the person who approves (T-066).
import { flushSync, mount, unmount } from "svelte";
import { afterEach, describe, expect, it } from "vitest";
import ImagePlanPart from "./ImagePlanPart.svelte";
import type { ImagePlan } from "../lib/types";

let app: ReturnType<typeof mount> | undefined;
afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  document.body.innerHTML = "";
});

const part = (notes?: string[]): ImagePlan => ({
  image: "web",
  build: {
    version: 2,
    trigger: "manual",
    upgrade: true,
    recipe_sha256: "a".repeat(64),
    resources: [],
  },
  removes: [],
  ...(notes ? { notes } : {}),
});

describe("ImagePlanPart notes", () => {
  it("shows each note of the image part", () => {
    app = mount(ImagePlanPart, {
      target: document.body,
      props: { part: part(["Image web: thin-pool metadata use is unknown."]) },
    });
    flushSync();
    const notes = [...document.querySelectorAll('p[role="note"]')].map((p) => p.textContent);
    expect(notes).toContain("Image web: thin-pool metadata use is unknown.");
  });
  it("shows no note when there is none", () => {
    app = mount(ImagePlanPart, { target: document.body, props: { part: part() } });
    flushSync();
    expect(document.body.textContent).not.toContain("thin-pool");
  });
});
