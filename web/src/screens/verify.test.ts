// @vitest-environment happy-dom
// T-154: Image verify (D-335, D-344, D-345) and "Refresh base" on the mock.
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMockApi } from "../lib/mock/mock";
import ImageForm from "./ImageForm.svelte";
import ImageView from "./ImageView.svelte";
import PlanView from "./PlanView.svelte";
import { button, el, planOpened, reset, show, text, type, until } from "./testkit";

afterEach(reset);

describe("Refresh base", () => {
  it("proposes a pin-only plan; the plan shows source, key, old and new", async () => {
    const api = createMockApi({ signedIn: true });
    show(ImageView, { api, name: "ubuntu-26.04" });
    await until(() => text().includes("Ubuntu cloud images, signed SHA256SUMS"));
    (await until(() => button("Refresh base"))).click();
    const id = await planOpened();
    const plan = await api.getPlan(id);
    expect(plan.origin).toBe("pin_refresh");
    // The first refresh has no stored signature time: a person approves (D-335).
    expect(plan.human_only).toEqual(["unverified-pin"]);
    reset();
    show(PlanView, { api, id });
    await until(() => text().includes("new base checksum"));
    expect(text()).toContain("Signed by the key D2EB44626FDDC30B513D5BB71A5D6C4C7DB87C81");
    expect(text()).toContain("None: this is the first refresh of this image.");
    expect(text()).toContain("New base checksum of an image (only the pin changes)");
    expect(text()).toContain("not proven by a newer signature");
    expect(el("#pin-old-0").textContent).toBe(plan.pins?.[0]?.old);
    expect(el("#pin-new-0").textContent).toBe(plan.pins?.[0]?.new);
  });

  it("says unchanged when the pin is current", async () => {
    const api = createMockApi({ signedIn: true, stepDelayMs: 0 });
    const first = await api.refreshImagePin("ubuntu-26.04");
    if (!("plan" in first)) throw new Error("expected a plan");
    await api.approvePlan(first.plan.id);
    await api.idle();
    show(ImageView, { api, name: "ubuntu-26.04" });
    (await until(() => button("Refresh base"))).click();
    await until(() => document.querySelector("#refresh-unchanged"));
    expect(text()).toContain("The pin is current");
  });

  it("is not offered without verify; a source error shows", async () => {
    const api = createMockApi({ signedIn: true });
    show(ImageView, { api, name: "debian-13" });
    await until(() => text().includes("none: a person checks a new checksum"));
    expect(button("Refresh base")).toBeUndefined();
    reset();
    vi.spyOn(api, "refreshImagePin").mockRejectedValue(new Error("signature check failed"));
    show(ImageView, { api, name: "ubuntu-26.04" });
    (await until(() => button("Refresh base"))).click();
    await until(() => text().includes("signature check failed"));
    expect(location.hash).toBe("");
  });
});

describe("Image form: verify and the checksum type", () => {
  it("saves verify and a SHA-512 pin for a Debian VM image", async () => {
    const api = createMockApi({ signedIn: true });
    const create = vi.spyOn(api, "createPlan");
    show(ImageForm, { api, name: "debian-13" });
    await until(() => document.querySelector("#i-verify"));
    type("#i-algo", "sha512");
    type("#i-sha", "a".repeat(128));
    type("#i-verify", "debian-https");
    expect(text()).toContain("every new pin needs a person");
    type("#i-reason", "Pin from SHA512SUMS.");
    button("Show the plan")?.click();
    await planOpened();
    const spec = (create.mock.calls[0]?.[0].put?.[0] as { spec: Record<string, unknown> }).spec;
    expect(spec["sha512"]).toBe("a".repeat(128));
    expect(spec["sha256"]).toBeUndefined();
    expect(spec["verify"]).toEqual({ source: "debian-https" });
  });

  it("refuses a source that does not fit address or checksum (D-344)", async () => {
    const api = createMockApi({ signedIn: true });
    const create = vi.spyOn(api, "createPlan");
    show(ImageForm, { api, name: "debian-13" });
    await until(() => document.querySelector("#i-verify"));
    type("#i-verify", "ubuntu-cloud");
    type("#i-reason", "Wrong source.");
    button("Show the plan")?.click();
    const err = await until(() => document.querySelector("#i-verify-error"));
    expect(err.textContent).toContain("cloud-images.ubuntu.com");
    type("#i-verify", "debian-https");
    button("Show the plan")?.click();
    await until(() => el("#i-verify-error").textContent?.includes("SHA-512"));
    expect(create).not.toHaveBeenCalled();
  });

  it("loads verify and the algorithm of an existing Image", async () => {
    const api = createMockApi({ signedIn: true });
    show(ImageForm, { api, name: "ubuntu-26.04" });
    await until(
      () => document.querySelector<HTMLSelectElement>("#i-verify")?.value === "ubuntu-cloud",
    );
    expect(el<HTMLSelectElement>("#i-algo").value).toBe("sha256");
  });
});
