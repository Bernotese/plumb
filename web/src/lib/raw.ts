// Raw mode (T-118, D-296): every override of a Target is a raw resource.
// The person sees the attributes it fixes, edits its `content` and can go
// back to the Block. Pure functions; the screen stays thin.
import { secretName } from "./schemaForm";
import type { TargetSpec } from "./types";

export interface RawResource {
  /** Resource id, for example `file:/etc/motd`. */
  id: string;
  /** The fixed attributes, sorted by name; `content` is separate. */
  attrs: { name: string; value: string }[];
  /** The fixed `content`, when the override holds a text. */
  content?: string;
}

/**
 * A value as one line of text: strings as they are, the rest as JSON. A
 * secret reference shows its name only, like a secret parameter in the
 * Target form; any other object with a `secret` key is masked (review L3 of
 * #227). The server refuses secret references in overrides; this is a
 * second line, so a value of a secret never reaches the page.
 */
export function valueText(v: unknown): string {
  if (typeof v === "string") return v;
  const name = secretName(v);
  if (name !== undefined) return `secret ${name}`;
  if (hasSecret(v)) return "(hidden: holds a secret reference)";
  return JSON.stringify(v);
}

/** True when `v` holds an object with a `secret` key at any depth. */
function hasSecret(v: unknown): boolean {
  if (typeof v !== "object" || v === null) return false;
  if (!Array.isArray(v) && "secret" in v) return true;
  return Object.values(v).some(hasSecret);
}

/** The raw resources of a Target spec, sorted by id. */
export function rawResources(spec: TargetSpec): RawResource[] {
  return Object.entries(spec.overrides ?? {})
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([id, o]) => {
      const fixed = (o ?? {}) as Record<string, unknown>;
      const content = fixed["content"];
      return {
        id,
        attrs: Object.keys(fixed)
          .filter((k) => !(k === "content" && typeof content === "string"))
          .sort()
          .map((name) => ({ name, value: valueText(fixed[name]) })),
        ...(typeof content === "string" ? { content } : {}),
      };
    });
}

/**
 * The spec with the override of `id` replaced: `content` set to a new text,
 * or the whole override removed with `null` ("Back to the Block"). The
 * other overrides and fields stay as stored.
 */
export function withRaw(spec: TargetSpec, id: string, content: string | null): TargetSpec {
  const stored = spec.overrides ?? {};
  const overrides =
    content === null
      ? Object.fromEntries(Object.entries(stored).filter(([k]) => k !== id))
      : { ...stored, [id]: { ...(stored[id] ?? {}), content } };
  return { ...spec, overrides };
}

/**
 * The raw offer of an Issue from an older controller without `Issue.raw`
 * (D-301): every resource that is not `guest:*` or `agent:*` comes from a Block.
 */
export function offersRaw(resource: string): boolean {
  return !resource.startsWith("guest:") && !resource.startsWith("agent:");
}
