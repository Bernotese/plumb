// Field errors of a form (QA-035-1): the 422 `errors` of a problem, keyed by
// JSON Pointer, merged with the form's own checks. A field shows its error
// next to it with aria-invalid and aria-describedby; ProblemBox lists only
// the errors that no field shows.
import { ApiError } from "./api";

/** Error messages of a 422 answer by pointer ("/name"); the first one per pointer wins. */
export function pointerErrors(err: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (!(err instanceof ApiError)) return out;
  for (const e of err.problem.errors ?? []) if (!(e.pointer in out)) out[e.pointer] = e.message;
  return out;
}

/** The aria-describedby value: the error id first, when there is an error. */
export function describedBy(
  id: string,
  error: string | undefined,
  hint = true,
): string | undefined {
  const ids = [error ? `${id}-error` : "", hint ? `${id}-hint` : ""].filter(Boolean);
  return ids.length ? ids.join(" ") : undefined;
}
