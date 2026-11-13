// Demo data of image builds (T-058 contract, T-064 screens) that the
// examples of api/openapi.yaml do not show: the Runs of the versions of
// ubuntu-web, a build that runs now (review N1 of T-058), a build that the
// disk check stopped (user answer 7, D-203) and a Target from a built version.
import { KNOWN_LXC_TEMPLATES } from "../images";
import type { ImageView, Run, RunStep, TargetView } from "../types";

export const GIB = 2 ** 30;

/** The text of a build that the disk check (D-203) did not start. */
export function diskRefusal(storage: string, after: number, limit: number): string {
  return `The build did not start: ${storage} would be ${after} % full with the build disk (limit ${limit} %). Nothing was made. Free space on ${storage} or lower retention.keep, then build again.`;
}

type StepSeed = [
  resource: string,
  from: string | null,
  to: string | null,
  status?: RunStep["status"],
];

function steps(seeds: StepSeed[], output: Record<string, string> = {}): RunStep[] {
  return seeds.map(([resource, from, to, status]) => ({
    resource,
    action: resource.startsWith("image:") || resource === "guest:config" ? null : "create",
    status: status ?? (to ? "applied" : "running"),
    changes: [],
    output: output[resource] ?? "",
    started_at: from,
    finished_at: to,
  }));
}

/** Build Runs: 15 (ubuntu-web v1), 17 (v2, stopped by the disk check), 18 (debian-tools v1, running), 19 (v3). */
export function buildRuns(): Run[] {
  const t = (day: number, h: number, m: number, s = 0) =>
    `2026-11-${String(day).padStart(2, "0")}T${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}Z`;
  const lane = (
    image: string,
    version: number,
    trigger: "change" | "manual" | "schedule",
    status: Run["status"],
    s: RunStep[],
  ) => ({ image, version, trigger, status, steps: s });
  const run = (
    id: number,
    plan: number,
    revision: number,
    status: Run["status"],
    start: string,
    end: string | null,
    images: Run["images"],
  ): Run => ({
    id,
    plan_id: plan,
    revision,
    status,
    started_at: start,
    finished_at: end,
    waits_for_restart: 0,
    targets: [],
    images,
  });
  const done = (day: number, h: number, base: number, first: RunStep["status"]): StepSeed[] => [
    ["image:base", t(day, h, base), t(day, h, base, 20), first],
    ["guest:config", t(day, h, base, 20), t(day, h, base, 48)],
    ["package:nginx", t(day, h, base + 3, 10), t(day, h, base + 3, 40)],
    ["service:nginx", t(day, h, base + 3, 40), t(day, h, base + 3, 41)],
    ["image:seal", t(day, h, base + 3, 41), t(day, h, base + 3, 58)],
    ["image:template", t(day, h, base + 3, 58), t(day, h, base + 4, 5)],
    ["image:retention", t(day, h, base + 4, 5), t(day, h, base + 5, 40), "unchanged"],
  ];
  const v1 = steps(done(6, 9, 0, "applied"), {
    "image:base": "plumb-images:import/plumb-ubuntu-web-3f1c0a9e5d6b.qcow2",
    "image:seal": "10 steps, guest powered off",
    "image:template": "plumb-img-ubuntu-web-v1",
  });
  const v2 = steps(
    [
      ["image:base", t(7, 10, 0), t(7, 10, 0, 1), "unchanged"],
      ["guest:config", t(7, 10, 0, 1), t(7, 10, 0, 1), "failed"],
      ["package:nginx", t(7, 10, 0, 1), t(7, 10, 0, 1), "skipped"],
      ["service:nginx", t(7, 10, 0, 1), t(7, 10, 0, 1), "skipped"],
      ["image:seal", t(7, 10, 0, 1), t(7, 10, 0, 1), "skipped"],
      ["image:template", t(7, 10, 0, 1), t(7, 10, 0, 1), "skipped"],
      ["image:retention", t(7, 10, 0, 1), t(7, 10, 0, 1), "skipped"],
    ],
    { "guest:config": diskRefusal("local-lvm", 87, 85) },
  );
  const v3 = steps(done(8, 3, 0, "unchanged"), {
    "image:seal": "10 steps, guest powered off",
    "image:template": "plumb-img-ubuntu-web-v3",
  });
  const running = steps([
    ["image:base", "2026-11-10T09:10:00Z", "2026-11-10T09:10:05Z", "unchanged"],
    ["guest:config", "2026-11-10T09:10:05Z", "2026-11-10T09:10:31Z"],
    ["file:/etc/motd", "2026-11-10T09:11:40Z", null],
    ["image:seal", null, null, "pending"],
    ["image:template", null, null, "pending"],
    ["image:retention", null, null, "pending"],
  ]);
  return [
    run(15, 8, 6, "succeeded", t(6, 9, 0), t(6, 9, 5, 40), [
      lane("ubuntu-web", 1, "change", "succeeded", v1),
    ]),
    run(17, 9, 7, "failed", t(7, 10, 0), t(7, 10, 0, 1), [
      lane("ubuntu-web", 2, "manual", "failed", v2),
    ]),
    run(18, 10, 7, "running", "2026-11-10T09:10:00Z", null, [
      lane("debian-tools", 1, "manual", "running", running),
    ]),
    run(19, 20, 7, "succeeded", t(8, 3, 0), t(8, 3, 6, 12), [
      lane("ubuntu-web", 3, "schedule", "succeeded", v3),
    ]),
  ];
}

/**
 * Changes the image examples in place: the failed v2 ends when the disk check
 * refused it, and debian-tools builds its first version now.
 */
export function seedImages(images: ImageView[]): void {
  const web = images.find((i) => i.document.name === "ubuntu-web");
  const v2 = web?.status?.versions.find((v) => v.version === 2);
  if (v2) v2.finished_at = "2026-11-07T10:00:01Z";
  const debian = images.find((i) => i.document.name === "debian-13");
  if (!web?.status || !debian || images.some((i) => i.document.name === "debian-tools")) return;
  images.push({
    revision: 7,
    document: {
      kind: "Image",
      name: "debian-tools",
      spec: {
        title: "Debian 13 with the admin tools",
        type: "vm",
        url: debian.document.spec.url,
        sha256: debian.document.spec.sha256,
        build: {
          blocks: [{ use: "motd", params: { text: "Built by Plumb." } }],
          upgrade: true,
          guest: { cores: 1, memory: 1024, disk: 8, bridge: "vmbr0", ip: "dhcp" },
        },
        retention: { keep: 2 },
      },
    },
    status: {
      versions: [
        {
          version: 1,
          status: "building",
          trigger: "manual",
          run_id: 18,
          revision: 7,
          recipe_sha256: "7d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e",
          base_sha256: debian.document.spec.sha256 ?? "",
          disk_gb: 8,
          started_at: "2026-11-10T09:10:00Z",
          finished_at: null,
          removed_at: null,
          targets: [],
        },
      ],
      building: 1,
      last_build: null,
      next_build_at: null,
      unused_imports: [],
      ...(web.status.disk ? { disk: structuredClone(web.status.disk) } : {}),
    },
  });
}

/** A Target cloned from ubuntu-web v3 (the `targets` of that version). */
export function seedTargets(targets: Map<string, TargetView>): void {
  const web1 = targets.get("web1");
  if (!web1 || targets.has("web3")) return;
  targets.set("web3", {
    revision: 7,
    document: {
      kind: "Target",
      name: "web3",
      spec: {
        type: "vm",
        image: "ubuntu-web",
        guest: { cores: 1, memory: 1024, disk: 10, bridge: "vmbr0", ip: "dhcp" },
        blocks: [],
      },
    },
    status: {
      ...structuredClone(web1.status),
      phase: "ready",
      guest_id: "9006",
      ips: [],
      reported_ips: ["192.0.2.46"],
      reported_at: "2026-11-10T09:00:10Z",
      open_issues: 0,
      waiting: [],
      image_version: 3,
      last_run_id: 3,
    },
  });
}

/**
 * An lxc Image (T-038, D-224) with one unused template file (D-233), and a
 * container Target from it (D-226: no bootstrap user, keys for root).
 */
export function seedLxc(images: ImageView[], targets: Map<string, TargetView>): void {
  if (images.some((i) => i.document.name === "debian-ct")) return;
  const debian = KNOWN_LXC_TEMPLATES[0];
  if (!debian) return;
  const old = "plumb-images:vztmpl/plumb-ct-debian-ct-0a1b2c3d4e5f.tar.gz";
  images.push({
    revision: 7,
    document: {
      kind: "Image",
      name: "debian-ct",
      spec: { title: "Debian 13 container", type: "lxc", url: debian.url, sha512: debian.sha512 },
    },
    status: {
      versions: [],
      building: null,
      last_build: null,
      next_build_at: null,
      unused_imports: [{ volume: old, size_bytes: 207696168, command: `pvesm free ${old}` }],
    },
  });
  // A built lxc Image (T-082): its versions pin the SHA-512 of the base (D-254).
  images.push({
    revision: 7,
    document: {
      kind: "Image",
      name: "debian-ct-web",
      spec: {
        title: "Debian 13 container with nginx",
        type: "lxc",
        url: debian.url,
        sha512: debian.sha512,
        build: {
          blocks: [{ use: "nginx", params: {} }],
          upgrade: true,
          guest: { cores: 1, memory: 512, disk: 4, bridge: "vmbr0", ip: "dhcp" },
        },
        retention: { keep: 2 },
      },
    },
    status: {
      versions: [
        {
          version: 1,
          status: "ready",
          trigger: "change",
          run_id: 3,
          revision: 7,
          recipe_sha256: "4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d",
          base_sha256: debian.sha512,
          disk_gb: 4,
          started_at: "2026-11-10T08:00:00Z",
          finished_at: "2026-11-10T08:04:30Z",
          removed_at: null,
          targets: ["ct-web1"],
        },
      ],
      building: null,
      last_build: null,
      next_build_at: null,
      unused_imports: [],
    },
  });
  const web1 = targets.get("web1");
  if (!web1 || targets.has("ct1")) return;
  // A container from debian-ct-web v1: no ssh_keys, the Block deploy-user (D-251).
  targets.set("ct-web1", {
    revision: 7,
    document: {
      kind: "Target",
      name: "ct-web1",
      spec: {
        type: "lxc",
        image: "debian-ct-web",
        guest: { cores: 1, memory: 512, disk: 4, bridge: "vmbr0", ip: "dhcp" },
        blocks: [
          {
            use: "deploy-user",
            params: {
              ssh_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlumbExampleKeyOnly admin@example",
            },
          },
        ],
      },
    },
    status: {
      ...structuredClone(web1.status),
      phase: "ready",
      guest_id: "9011",
      ips: [],
      reported_ips: ["192.0.2.48"],
      reported_at: "2026-11-10T09:00:10Z",
      open_issues: 0,
      waiting: [],
      image_version: 1,
      last_run_id: 3,
    },
  });
  targets.set("ct1", {
    revision: 7,
    document: {
      kind: "Target",
      name: "ct1",
      spec: {
        type: "lxc",
        image: "debian-ct",
        guest: {
          cores: 1,
          memory: 512,
          disk: 4,
          bridge: "vmbr0",
          ip: "dhcp",
          ssh_keys: ["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlumbExampleKeyOnly admin@example"],
        },
        blocks: [],
      },
    },
    status: {
      ...structuredClone(web1.status),
      phase: "ready",
      guest_id: "9010",
      ips: [],
      reported_ips: ["192.0.2.47"],
      reported_at: "2026-11-10T09:00:10Z",
      open_issues: 0,
      waiting: [],
      image_version: null,
      last_run_id: 3,
    },
  });
}
