// Typed access to the response examples of api/openapi.yaml
// (examples.gen.json is written by `npm run gen`, see scripts/gen-examples.mjs).
import raw from "./examples.gen.json";
import type { operations } from "../api.gen";

type Responses<Op extends keyof operations> = operations[Op]["responses"];
type Body<R> = R extends { content: infer C } ? C[keyof C] : never;

const all = raw as unknown as Record<string, Record<string, unknown>>;

/** A deep copy of the example of one operation and status code. */
export function example<Op extends keyof operations, C extends keyof Responses<Op> & number>(
  op: Op,
  code: C,
): Body<Responses<Op>[C]> {
  const v = all[op]?.[String(code)];
  if (v === undefined) throw new Error(`no example for ${op} ${code}`);
  return structuredClone(v) as Body<Responses<Op>[C]>;
}

/**
 * The items of a list example, typed with the generated item type.
 * (A list response is Page & {items: T[]}; the generator keeps Page's
 * untyped items, so the item type is given here.)
 */
export function exampleItems<T>(page: { items: unknown }): T[] {
  return page.items as T[];
}
