// @vitest-environment happy-dom
// T-082: builds of lxc Images in the web (D-234, D-251, D-252, D-254).
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { basePin, KNOWN_LXC_TEMPLATES } from "../lib/images";
import { createMockApi } from "../lib/mock/mock";
import type { ImageDoc, TargetDoc } from "../lib/types";
import ImageForm from "./ImageForm.svelte";
import ImageScreen from "./ImageView.svelte";
import TargetForm from "./TargetForm.svelte";
import { el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });
const debian = KNOWN_LXC_TEMPLATES.find((t) => t.label === "Debian 13");
const KEY = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJT082testkey root-test";

describe("lxc image build form", () => {
  it("makes a built lxc Image: container texts, no user, the upgrade of D-252", async () => {
    if (!debian) throw new Error("missing Debian template");
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    expect(document.querySelector("#i-lxc-keys")).toBeNull();
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    expect(el("#i-lxc-keys").textContent).toContain("deploy-user");
    expect(el("#i-upgrade-hint").textContent).toContain("apt-get dist-upgrade");
    expect(el("#i-upgrade-hint").textContent).toContain("old configuration files");
    expect(text()).toContain("root file system of the build container, at least 2");
    expect(text()).toContain("CIDR form for the build container.");
    expect(text()).not.toContain("for the build guest");
    expect(text()).not.toContain("1.4 GB (Debian)");
    expect(document.querySelector("#i-guest-user")).toBeNull();
    type("#i-known", "Debian 13");
    type("#i-name", "debian-ct-nginx");
    type("#i-reason", "A container template with nginx.");
    el<HTMLFormElement>("form").requestSubmit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as ImageDoc;
    expect(doc.spec.type).toBe("lxc");
    expect(doc.spec.sha512).toBe(debian.sha512);
    expect(doc.spec.build?.upgrade).toBe(true);
    expect(doc.spec.build?.guest).not.toHaveProperty("user");
    expect(doc.spec.retention).toEqual({ keep: 3 });
  });

  it("restores the container build section and accepts exactly 2 GiB with upgrade off", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    expect(document.querySelector("#i-guest-disk")).toBeNull();
    for (const enabled of [true, false, true]) {
      el<HTMLInputElement>("#i-build").click();
      flushSync();
      expect(document.querySelector("#i-guest-disk") !== null).toBe(enabled);
      expect(document.querySelector("#i-upgrade") !== null).toBe(enabled);
    }
    expect(text(el("#i-upgrade-hint"))).toContain("apt-get update");
    expect(text(el("#i-upgrade-hint"))).toContain("Off: the packages of the Proxmox template.");
    el<HTMLInputElement>("#i-upgrade").click();
    flushSync();
    type("#i-known", "Debian 13");
    type("#i-name", "minimum-ct");
    type("#i-guest-disk", "2");
    type("#i-reason", "Build at the container disk minimum.");
    el<HTMLFormElement>("form").requestSubmit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as ImageDoc;
    expect(doc.spec.build?.guest.disk).toBe(2);
    expect(doc.spec.build?.upgrade).toBe(false);
  });

  it("refuses a build disk under 2 GiB for a container, not for a VM", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>('input[name="i-type"][value="lxc"]').click();
    flushSync();
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    type("#i-known", "Debian 13");
    type("#i-name", "tiny-ct");
    type("#i-guest-disk", "1");
    type("#i-reason", "Small.");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    expect(text()).toContain("needs at least 2 GiB");
  });
});

describe("build of an lxc Image with keyed Targets (N1 of the #176 review)", () => {
  it("warns that the server refuses the build while a Target has SSH keys", async () => {
    show(ImageForm, { api: adminApi(), name: "debian-ct" });
    await until(() => document.querySelector("#i-build"));
    expect(document.querySelector("#i-lxc-keyed-targets")).toBeNull();
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    expect(el("#i-lxc-keyed-targets").textContent).toContain("ct1");
  });

  it("does not warn for an Image whose Targets have no keys", async () => {
    show(ImageForm, { api: adminApi(), name: "debian-ct-web" });
    await until(() => document.querySelector("#i-lxc-keys"));
    expect(document.querySelector("#i-lxc-keyed-targets")).toBeNull();
  });
});

describe("target from a built lxc Image (D-251)", () => {
  it("has no SSH key field and names the Block deploy-user", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#t-image"));
    type("#t-image", "debian-ct");
    expect(document.querySelector("#guest-ssh_keys-add")).not.toBeNull();
    expect(document.querySelector("#t-lxc-keys")).toBeNull();
    el<HTMLButtonElement>("#guest-ssh_keys-add").click();
    flushSync();
    type("#guest-ssh_keys-0", KEY);
    type("#t-image", "debian-ct-web");
    // N2 of the #176 review: the dropped key is said, not hidden.
    expect(el("#t-keys-dropped").textContent).toMatch(/SSH key you typed[\s\S]*is not sent/);
    type("#t-image", "debian-ct");
    expect(document.querySelector("#t-keys-dropped")).toBeNull();
    expect(el<HTMLInputElement>("#guest-ssh_keys-0").value).toBe(KEY);
    type("#t-image", "debian-ct-web");
    expect(document.querySelector("#guest-ssh_keys-add")).toBeNull();
    expect(text()).not.toContain("SSH keys for root");
    expect(el("#t-lxc-keys").textContent).toContain("deploy-user");
    el<HTMLButtonElement>("#t-add-deploy-user").click();
    flushSync();
    expect(document.querySelector("#t-add-deploy-user")).toBeNull();
    type('[id^="use-"][id$="-ssh_key"]', KEY);
    type("#t-name", "ct-web9");
    type("#t-reason", "A container from the built image.");
    el<HTMLFormElement>("form").requestSubmit();
    await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
    expect(doc.spec.type).toBe("lxc");
    expect(doc.spec.image).toBe("debian-ct-web");
    expect(doc.spec.guest).not.toHaveProperty("ssh_keys");
    expect(doc.spec.guest).not.toHaveProperty("user");
    expect(doc.spec.blocks).toEqual([
      { use: "deploy-user", params: { name: "deploy", ssh_key: KEY } },
    ]);
  });

  it.each([false, true])(
    "submits a Target after entering keys on a plain Image (built=%s)",
    async (built) => {
      const api = adminApi();
      const createPlan = vi.spyOn(api, "createPlan");
      show(TargetForm, { api });
      await until(() => document.querySelector("#t-image"));
      type("#t-image", "debian-ct");
      el<HTMLButtonElement>("#guest-ssh_keys-add").click();
      flushSync();
      type("#guest-ssh_keys-0", KEY);
      if (built) type("#t-image", "debian-ct-web");
      type("#t-name", "ct-key-check");
      type("#t-reason", "Check create-time SSH keys.");
      el<HTMLFormElement>("form").requestSubmit();
      await planOpened();
      const doc = createPlan.mock.calls[0]?.[0].put?.[0] as TargetDoc;
      expect(doc.spec.image).toBe(built ? "debian-ct-web" : "debian-ct");
      if (built) expect(doc.spec.guest).not.toHaveProperty("ssh_keys");
      else expect(doc.spec.guest.ssh_keys).toEqual([KEY]);
    },
  );

  it("says so when there is no Block deploy-user", async () => {
    const api = adminApi();
    const blocks = await api.listBlocks();
    vi.spyOn(api, "listBlocks").mockResolvedValue(
      blocks.filter((b) => b.document.name !== "deploy-user"),
    );
    show(TargetForm, { api });
    await until(() => document.querySelector("#t-image"));
    type("#t-image", "debian-ct-web");
    expect(document.querySelector("#t-add-deploy-user")).toBeNull();
    expect(el("#t-lxc-keys").textContent).toMatch(/no Block\s+deploy-user\s+yet/);
  });

  it("the mock refuses keys like the server", async () => {
    const api = adminApi();
    const doc: TargetDoc = {
      kind: "Target",
      name: "ct-web2",
      spec: {
        type: "lxc",
        image: "debian-ct-web",
        guest: { cores: 1, memory: 512, disk: 4, bridge: "vmbr0", ip: "dhcp", ssh_keys: [KEY] },
        blocks: [],
      },
    };
    await expect(api.createPlan({ reason: "keys", put: [doc] })).rejects.toMatchObject({
      problem: { errors: [{ pointer: "/put/0/spec/guest/ssh_keys" }] },
    });
  });
});

describe("built lxc image page", () => {
  it("shows Built, the container recipe and the SHA-512 base pin (D-254)", async () => {
    if (!debian) throw new Error("missing Debian template");
    show(ImageScreen, { api: adminApi(), name: "debian-ct-web" });
    await until(() => rows().length > 0);
    expect(text()).toContain("Plumb makes container templates from the Proxmox template.");
    expect(text()).toContain("apt-get dist-upgrade runs in the build container");
    expect(text()).toContain("Build container");
    expect(text()).not.toContain("A stock container image has no versions");
    const pin = el(".base-pin");
    expect(pin.textContent).toContain(`base sha512 ${debian.sha512.slice(0, 12)}…`);
    expect(pin.getAttribute("title")).toMatch(/^[0-9a-f]{128}$/);
    expect(pin.getAttribute("title")).toBe(debian.sha512);
  });

  it("a vm version shows its SHA-256 base", async () => {
    show(ImageScreen, { api: adminApi(), name: "ubuntu-web" });
    await until(() => document.querySelector(".base-pin"));
    expect(el(".base-pin").textContent).toMatch(/^base sha256 [0-9a-f]{12}…$/);
  });

  it("basePin takes 64 or 128 hex only", () => {
    expect(basePin("a".repeat(64))).toEqual({ algo: "sha256", short: "a".repeat(12) });
    expect(basePin("b".repeat(128))).toEqual({ algo: "sha512", short: "b".repeat(12) });
    for (const bad of ["", "a".repeat(63), "a".repeat(96), "A".repeat(64), null, undefined])
      expect(basePin(bad)).toBeNull();
  });
});
