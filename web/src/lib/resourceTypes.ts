import resourceKinds from "./resourceKinds.json";

/**
 * The resource types of internal/resource: the target kinds of each type
 * (D-260) and the attribute that forms its id (`Type.Key`: "file:<path>").
 * One source for the web; a Go test keeps resourceKinds.json equal to the
 * table in internal/resource/resource.go.
 */
const TYPES: Record<string, { kinds: readonly string[]; key: string }> = resourceKinds;

/** The target kinds of each resource type. */
export const KINDS: Record<string, readonly string[]> = Object.fromEntries(
  Object.entries(TYPES).map(([t, v]) => [t, v.kinds]),
);

/** The attribute that forms the id of a resource of this type; "name" for an unknown type. */
export const keyAttr = (type: string): string => TYPES[type]?.key ?? "name";

/** The resource type names, sorted. */
export const typeNames = (): string[] => Object.keys(TYPES).sort();
