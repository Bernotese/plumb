// Parse the YAML text of a Block in the browser (D-261, D-263) and find the
// line of a JSON Pointer, so a schema error or a 422 of the server shows at
// its line. YAML 1.2 core schema: `yes`, `no`, `on`, `off` stay strings and
// `0644` is the number 644 (quote a file mode). No anchors or aliases, no
// custom tags and one document only, like the import (D-095).
import { checkBlock } from "./blockCheck";
import { MAX_TEXT, parseYaml, sortErrors, tokens, type LineError, type Parsed } from "./yaml";

// The strict parser lives in yaml.ts (shared with the YAML mode, D-363).
export { MAX_TEXT, tokens, type LineError, type Parsed };

/** Parses the text, then checks it against block.json. */
export function parseBlock(text: string): Parsed {
  const parsed = parseYaml(text);
  if (parsed.value === undefined || parsed.value === null || parsed.errors.length > 0)
    return parsed;
  const errors = [...parsed.errors];
  for (const e of checkBlock(parsed.value)) {
    errors.push({ line: parsed.lineOf(e.at), message: e.message, pointer: e.pointer });
  }
  return { ...parsed, errors: sortErrors(errors) };
}
