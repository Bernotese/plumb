// @vitest-environment happy-dom
// T-081: lxc Images and container Targets in the web (T-038 §8).
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { KNOWN_LXC_TEMPLATES, safeFreeCommand } from "../lib/images";
import { createMockApi } from "../lib/mock/mock";
import type { ImageDoc, TargetDoc } from "../lib/types";
import { forKind, REENROLL_REFUSED } from "../lib/view";
import ImageForm from "./ImageForm.svelte";
import ImageScreen from "./ImageView.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const debian = KNOWN_LXC_TEMPLATES.find((t) => t.label === "Debian 13") ?? {
  label: "",
  url: "",
  sha512: "",
};

describe("lxc image form", () => {
  it("makes an lxc Image from a known template: http, SHA-512, no build", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    expect(document.querySelector("#i-build")).toBeNull();
    expect(text()).toContain("SHA-512 checksum");
    expect(text()).toContain("signed Proxmox appliance index");
    type("#i-known", "Debian 13");
    expect(el<HTMLInputElement>("#i-url").value).toBe(debian.url);
    expect(el<HTMLInputElement>("#i-sha").value).toBe(debian.sha512);
    type("#i-name", "debian-ct2");
    type("#i-reason", "Containers.");
    el<HTMLFormElement>("form").requestSubmit();
    const id = await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as ImageDoc;
    expect(doc.spec).toEqual({ type: "lxc", url: debian.url, sha512: debian.sha512 });
    expect((await api.getPlan(id)).human_only).toContain("image-recipe");
  });

  it("checks url and pin by type", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    type("#i-name", "ct");
    type("#i-url", "ftp://x");
    type("#i-sha", "c".repeat(64)); // a SHA-256 is not a SHA-512
    type("#i-reason", "x");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("http:// or https:// address of the Proxmox template");
    expect(text()).toContain("128 characters");
    // A VM Image still needs https.
    el<HTMLInputElement>('input[name="i-type"][value="vm"]').click();
    flushSync();
    type("#i-url", "http://cloud.debian.org/x.qcow2");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(text()).toContain("Give an https:// address");
    expect(createPlan).not.toHaveBeenCalled();
  });

  it("edits an lxc Image: type fixed, SHA-512 filled", async () => {
    show(ImageForm, { api: adminApi(), name: "debian-ct" });
    await until(() => document.querySelector("#i-sha"));
    expect(el("#i-type").textContent).toBe("Container (LXC)");
    expect(el<HTMLInputElement>("#i-sha").value).toBe(debian.sha512);
    expect(document.querySelector('input[name="i-type"]')).toBeNull();
    expect(document.querySelector("#i-build")).toBeNull();
  });
});

describe("lxc image page", () => {
  it("shows the SHA-512 and the unused template files with the pvesm line (D-233)", async () => {
    show(ImageScreen, { api: adminApi(), name: "debian-ct" });
    await until(() => text().includes("Unused template files"));
    expect(el("#image-type").textContent).toBe("Container (LXC)");
    expect(el("#image-sum").textContent?.trim()).toBe(`sha512 ${debian.sha512}`);
    expect(text()).not.toContain("undefined");
    expect(text()).toContain("Plumb never deletes a template file");
    expect(text()).toContain(
      "pvesm free plumb-images:vztmpl/plumb-ct-debian-ct-0a1b2c3d4e5f.tar.gz",
    );
    expect(button(/^Copy/)).toBeDefined();
    expect(text()).toContain("A container image has no versions");
  });

  it("offers Copy only for Plumb's own template name", () => {
    const ok = "pvesm free plumb-images:vztmpl/plumb-ct-debian-ct-0a1b2c3d4e5f.tar.gz";
    expect(safeFreeCommand(ok)).toBe(ok);
    for (const bad of [
      "pvesm free plumb-images:vztmpl/debian-13-standard_13.6-1_amd64.tar.zst",
      "pvesm free plumb-images:vztmpl/plumb-ct-x-0a1b2c3d4e5f.tar.gz; rm -rf /",
      "pvesm free plumb-images:vztmpl/plumb-ct-x-0a1b2c3d4e5.tar.gz",
      "pvesm free plumb-images:vztmpl/plumb-ct-X-0a1b2c3d4e5f.tar.gz",
      "pvesm free plumb-images:import/plumb-ct-x-0a1b2c3d4e5f.tar.gz",
    ])
      expect(safeFreeCommand(bad)).toBeNull();
  });
});

describe("container target form", () => {
  it("follows the Image type: no user, keys for root, type lxc in the spec", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#t-image"));
    expect(el("#t-type").textContent).toBe("Virtual machine");
    expect(document.querySelector("#guest-user")).not.toBeNull();
    type("#t-image", "debian-ct");
    expect(el("#t-type").textContent).toBe("Container (LXC)");
    expect(document.querySelector("#guest-user")).toBeNull();
    expect(text()).toContain("SSH keys for root");
    expect(text()).toContain("takes cores and memory at once");
    type("#t-name", "ct2");
    type("#guest-disk", "4");
    type("#t-reason", "A container.");
    el<HTMLFormElement>("form").requestSubmit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.type).toBe("lxc");
    expect(doc.spec.image).toBe("debian-ct");
    expect(doc.spec.guest).not.toHaveProperty("user");
  });

  it("an existing container: Container, no Restart needed, container wording", async () => {
    const api = adminApi();
    const t = await api.getTarget("ct1");
    t.status.waiting = [{ attr: "cores", from: 1, to: 2, since: "2026-11-10T09:00:00Z" }] as never;
    vi.spyOn(api, "getTarget").mockResolvedValue(t);
    show(TargetForm, { api, name: "ct1" });
    await until(() => document.querySelector("#t-type"));
    expect(el("#t-type").textContent).toBe("Container (LXC)");
    expect(document.querySelector("#waiting-title")).toBeNull();
    expect(document.querySelector("#guest-user")).toBeNull();
    expect(text()).toContain("next stop and start of the container");
    expect(text()).not.toMatch(/\bVM\b/);
  });
});

describe("texts", () => {
  it("forKind says container for an lxc guest", () => {
    expect(forKind(REENROLL_REFUSED, "lxc")).toContain("Stop the container in Proxmox");
    expect(forKind(REENROLL_REFUSED, "lxc")).not.toMatch(/\bVM\b/);
    expect(forKind(REENROLL_REFUSED, "vm")).toBe(REENROLL_REFUSED);
  });

  it("the known templates equal the table of docs/ARCHITECTURE.md", () => {
    // vitest runs in web/.
    const doc = readFileSync(join(process.cwd(), "..", "docs", "ARCHITECTURE.md"), "utf8");
    for (const t of KNOWN_LXC_TEMPLATES) {
      expect(doc).toContain(`| ${t.label} | \`${t.url}\` | \`${t.sha512}\` |`);
    }
  });
});
