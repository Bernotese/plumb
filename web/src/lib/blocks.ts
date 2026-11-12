// Helpers of the Blocks screens (T-096). Pure functions, so the screens stay thin.
import type { BlockDoc, ImageView, TargetView } from "./types";

/** The text of a new Block: a small valid example the user changes. */
export const NEW_BLOCK = `kind: Block
name: my-block
spec:
  title: My block
  description: What this Block does.
  params:
    type: object
    properties:
      text:
        type: string
        title: Text
        default: Managed by Plumb.
  resources:
    - file:
        path: /etc/my-block.txt
        content: |
          {{ .text }}
        mode: "0644"
`;

export interface Users {
  targets: string[];
  images: string[];
}

/** The Targets and Images that use a Block, sorted by name. */
export function usersOf(block: string, targets: TargetView[], images: ImageView[]): Users {
  return {
    targets: targets
      .filter((t) => t.document.spec.blocks?.some((u) => u.use === block))
      .map((t) => t.document.name)
      .sort(),
    images: images
      .filter((i) => i.document.spec.build?.blocks?.some((u) => u.use === block))
      .map((i) => i.document.name)
      .sort(),
  };
}

/** The resource types of a Block in order, each once ("file, package"). */
export function resourceTypes(doc: BlockDoc): string[] {
  const out: string[] = [];
  for (const r of doc.spec.resources) {
    for (const t of Object.keys(r)) if (!out.includes(t)) out.push(t);
  }
  return out;
}

export interface ParamRow {
  name: string;
  type: string;
  required: boolean;
  secret: boolean;
  default?: string;
  title?: string;
}

/** The parameters of a Block as table rows, in the order of the schema. */
export function paramRows(doc: BlockDoc): ParamRow[] {
  const params = doc.spec.params;
  const required = params?.required ?? [];
  return Object.entries(params?.properties ?? {}).map(([name, raw]) => {
    const p = (raw ?? {}) as {
      type?: string | string[];
      title?: string;
      default?: unknown;
      writeOnly?: boolean;
    };
    return {
      name,
      type: Array.isArray(p.type) ? p.type.join(" | ") : (p.type ?? "any"),
      required: required.includes(name),
      secret: p.writeOnly === true,
      ...(p.default !== undefined ? { default: JSON.stringify(p.default) } : {}),
      ...(p.title ? { title: p.title } : {}),
    };
  });
}

/** The 422 pointers of the first put document, without the "/put/0" prefix. */
export function docPointers(errors: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [p, m] of Object.entries(errors)) {
    if (p === "/put/0" || p.startsWith("/put/0/")) out[p.slice("/put/0".length)] = m;
  }
  return out;
}

export interface FieldError {
  pointer: string;
  message: string;
}

/** A render error of other documents that names a resource of this Block (D-265). */
export interface UseError {
  /** JSON Pointer into the Block. */
  pointer: string;
  message: string;
  /** The documents whose render failed, for example "Target web1". */
  docs: string[];
}

const DOC = /^(\w+) "([^"]+)": ([\s\S]*)$/;

/**
 * The server renders every Target and Image that uses a changed Block and
 * checks each resource there (templates, attribute schemas). Such a 422
 * points into the Target, and its message names the Block resource:
 * `Target "web1": resource file:/etc/motd from /spec/blocks/0 (Block "motd"
 * /spec/resources/0): attribute /mode: got number, want string`. This finds
 * the Block pointer in the message, so the editor shows it at the line, and
 * groups the same error of several documents. `errors` that do not name this
 * Block are not returned.
 */
export function useErrors(
  errors: FieldError[],
  block: string,
  doc: unknown,
): { found: UseError[]; handled: FieldError[] } {
  const ref = new RegExp(
    `Block "${block.replace(/[.]/g, "\\.")}" (/spec/resources/(\\d+)(?:/[^\\s:)]+)*)\\)?:? ?`,
  );
  const resources = (doc as { spec?: { resources?: unknown } } | undefined)?.spec?.resources;
  const found: UseError[] = [];
  const handled: FieldError[] = [];
  for (const e of errors) {
    if (e.pointer === "/put/0" || e.pointer.startsWith("/put/0/")) continue;
    const m = DOC.exec(e.message);
    const text = m?.[3] ?? e.message;
    const r = ref.exec(text);
    if (!r) continue;
    let pointer = r[1] ?? "";
    let detail = text.slice(r.index + r[0].length).trim();
    const attr = /^attribute (\/\S*)?:\s*/.exec(detail);
    if (attr && pointer === `/spec/resources/${r[2]}`) {
      const entry = Array.isArray(resources) ? resources[Number(r[2])] : undefined;
      const type =
        entry && typeof entry === "object" ? (Object.keys(entry as object)[0] ?? "") : "";
      pointer += type ? `/${type.replace(/~/g, "~0").replace(/\//g, "~1")}${attr[1] ?? ""}` : "";
      detail = `${attr[1] ? `${attr[1].slice(1)}: ` : ""}${detail.slice(attr[0].length)}`;
    }
    detail ||= "invalid";
    const docName = m ? `${m[1]} ${m[2]}` : "";
    const same = found.find((f) => f.pointer === pointer && f.message === detail);
    if (same) {
      if (docName && !same.docs.includes(docName)) same.docs.push(docName);
    } else found.push({ pointer, message: detail, docs: docName ? [docName] : [] });
    handled.push(e);
  }
  return { found, handled };
}
