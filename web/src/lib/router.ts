// Hash routes of the single-page app. No router library (D-019 dependency rule).

export type Route =
  | { name: "login" }
  | { name: "password" }
  | { name: "targets" }
  | { name: "target-new" }
  | { name: "target-edit"; target: string }
  | { name: "plan"; id: number }
  | { name: "run"; id: number }
  | { name: "issues" }
  | { name: "issue"; id: number }
  | { name: "not-found"; path: string };

const NAME = /^[a-z0-9]([a-z0-9.-]{0,61}[a-z0-9])?$/;

export function parse(hash: string): Route {
  const path = hash.replace(/^#/, "") || "/targets";
  const parts = path.split("/").filter(Boolean);
  const id = (s: string | undefined) => (s && /^[1-9][0-9]{0,15}$/.test(s) ? Number(s) : undefined);
  const [a, b, c] = parts;
  if (parts.length === 1 && a === "login") return { name: "login" };
  if (parts.length === 1 && a === "password") return { name: "password" };
  if (parts.length === 1 && a === "targets") return { name: "targets" };
  if (parts.length === 2 && a === "targets" && b === "new") return { name: "target-new" };
  if (parts.length === 3 && a === "targets" && b && NAME.test(b) && c === "edit") {
    return { name: "target-edit", target: b };
  }
  if (parts.length === 2 && a === "plans" && id(b)) return { name: "plan", id: id(b)! };
  if (parts.length === 2 && a === "runs" && id(b)) return { name: "run", id: id(b)! };
  if (parts.length === 1 && a === "issues") return { name: "issues" };
  if (parts.length === 2 && a === "issues" && id(b)) return { name: "issue", id: id(b)! };
  return { name: "not-found", path };
}

export function href(r: Route): string {
  switch (r.name) {
    case "login":
      return "#/login";
    case "password":
      return "#/password";
    case "targets":
      return "#/targets";
    case "target-new":
      return "#/targets/new";
    case "target-edit":
      return `#/targets/${encodeURIComponent(r.target)}/edit`;
    case "plan":
      return `#/plans/${r.id}`;
    case "run":
      return `#/runs/${r.id}`;
    case "issues":
      return "#/issues";
    case "issue":
      return `#/issues/${r.id}`;
    case "not-found":
      return `#${r.path}`;
  }
}

export function go(r: Route): void {
  window.location.hash = href(r);
}
