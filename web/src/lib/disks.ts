// Data disk rules that the web shares (T-239). The source is
// internal/state/disks.go: keep this list equal to its systemTrees.

/** The top directories that a container disk must not mount over or under; a "lib…" top counts too. */
export const SYSTEM_TREES: readonly string[] = [
  "bin",
  "boot",
  "dev",
  "etc",
  "proc",
  "root",
  "run",
  "sbin",
  "sys",
  "usr",
  "var",
];

/** The first part of a path is a system tree (disks.go diskPathProblem). */
export const isSystemTree = (top: string) => SYSTEM_TREES.includes(top) || top.startsWith("lib");
