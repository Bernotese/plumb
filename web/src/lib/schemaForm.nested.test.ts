// T-177 (D-363): nested forms for object and array-of-object parameters,
// key/value rows, and a YAML field for every schema a form cannot show.
import { describe, expect, it } from "vitest";
import {
  fieldsOf,
  initialValues,
  mergeParams,
  newItem,
  newRow,
  toParams,
  validate,
  type Item,
  type Row,
  type Values,
} from "./schemaForm";

const schema = {
  type: "object",
  required: ["site"],
  properties: {
    site: {
      type: "object",
      required: ["name"],
      properties: {
        name: { type: "string", pattern: "^[a-z]+$" },
        tls: {
          type: "object",
          properties: {
            cert: { type: "string" },
            // depth 3 object: no deeper form, a YAML field
            extra: { type: "object", properties: { a: { type: "string" } } },
          },
        },
      },
    },
    upstreams: {
      type: "array",
      maxItems: 3,
      items: {
        type: "object",
        required: ["host"],
        properties: {
          host: { type: "string" },
          port: { type: "integer", minimum: 1, maximum: 65535, default: 80 },
          token: { type: "string", writeOnly: true },
        },
      },
    },
    labels: { type: "object", additionalProperties: { type: "string", maxLength: 5 } },
    limits: { type: "object", additionalProperties: { type: "integer", minimum: 0 } },
    ports: { type: "array", items: { type: "integer" } },
    choice: { oneOf: [{ type: "string" }, { type: "integer" }] },
    optional: { type: "object", properties: { x: { type: "string", default: "d" } } },
  },
};

describe("nested schema forms (D-363)", () => {
  const fields = fieldsOf(schema);
  const byName = (n: string) => {
    const f = fields.find((x) => x.name === n);
    if (!f) throw new Error(`no field ${n}`);
    return f;
  };

  it("maps objects, arrays of objects, maps and the rest to kinds", () => {
    expect(fields.map((f) => [f.name, f.kind])).toEqual([
      ["site", "object"],
      ["upstreams", "objects"],
      ["labels", "map"],
      ["limits", "map"],
      ["ports", "yaml"],
      ["choice", "yaml"],
      ["optional", "object"],
    ]);
    const site = byName("site");
    expect(site?.fields?.map((f) => [f.name, f.kind])).toEqual([
      ["name", "text"],
      ["tls", "object"],
    ]);
    // depth 3: an object inside a depth-2 object is a YAML field
    expect(site?.fields?.[1]?.fields?.map((f) => [f.name, f.kind])).toEqual([
      ["cert", "text"],
      ["extra", "yaml"],
    ]);
    expect(byName("upstreams")?.fields?.map((f) => f.kind)).toEqual(["text", "integer", "secret"]);
    expect(byName("limits")?.value?.kind).toBe("integer");
  });

  it("starts from stored values; an unset optional object stays unset", () => {
    const v = initialValues(fields, {
      site: { name: "web", tls: { cert: "c", extra: { a: "x" } } },
      upstreams: [{ host: "a", port: 8080, token: { secret: "up-token" } }],
      labels: { env: "prod" },
      ports: [80, 443],
    });
    expect(v["site"]).toEqual({ name: "web", tls: { cert: "c", extra: "a: x\n" } });
    const items = v["upstreams"] as Item[];
    expect(items.map((i) => i.values)).toEqual([{ host: "a", port: "8080", token: "up-token" }]);
    expect((v["labels"] as Row[]).map((r) => [r.name, r.value])).toEqual([["env", "prod"]]);
    expect(v["ports"]).toBe("- 80\n- 443\n");
    expect(v["optional"]).toBeNull();
    expect(v["choice"]).toBe("");
  });

  it("writes typed parameters back, and the same document when nothing changed", () => {
    const stored = {
      site: { name: "web", tls: { cert: "c" } },
      upstreams: [{ host: "a", port: 8080, token: { secret: "up-token" } }],
      labels: { env: "prod" },
      limits: { cpu: 2 },
      ports: [80, 443],
      choice: 7,
    };
    const v = initialValues(fields, stored);
    expect(validate(fields, v)).toEqual({});
    expect(mergeParams(stored, fields, v)).toEqual(stored);
  });

  it("adds, moves and removes items; keeps keys the form cannot show", () => {
    const f = byName("upstreams");
    const v = initialValues(fields, {
      site: { name: "web" },
      upstreams: [{ host: "a", weight: 5 }],
    });
    const items = v["upstreams"] as Item[];
    const b = newItem(f);
    (b.values as Values)["host"] = "b";
    v["upstreams"] = [b, ...items]; // add, then move to the top
    const out = toParams(fields, v);
    expect(out["upstreams"]).toEqual([
      { host: "b", port: 80 },
      { host: "a", port: 80, weight: 5 },
    ]);
    v["upstreams"] = items.slice(1); // remove the last one
    expect(toParams(fields, v)["upstreams"]).toBeUndefined();
  });

  it("keeps unknown keys of a stored nested object", () => {
    const stored = { site: { name: "web", future: true } };
    const v = initialValues(fields, stored);
    expect(mergeParams(stored, fields, v)["site"]).toEqual({ name: "web", future: true });
  });

  it("reports errors at the path of the nested field", () => {
    const v = initialValues(fields, {});
    expect(v["site"]).toEqual({ name: "", tls: null });
    (v["site"] as Values)["name"] = "Web";
    const up = byName("upstreams");
    v["upstreams"] = [newItem(up), newItem(up), newItem(up), newItem(up)];
    ((v["upstreams"] as Item[])[1]?.values as Values)["port"] = "0";
    const labels = byName("labels");
    const r1 = newRow(labels);
    const r2 = newRow(labels);
    r1.name = "env";
    r1.value = "toolong";
    r2.name = "env";
    v["labels"] = [r1, r2];
    v["ports"] = "- 80\n  - x: [";
    const e = validate(fields, v);
    expect(e["site/name"]).toBe("The value has the wrong format.");
    expect(e["upstreams"]).toBe("Use at most 3 entries.");
    expect(e["upstreams/0/host"]).toBe("This field is required.");
    expect(e["upstreams/1/port"]).toBe("Use 1 or more.");
    expect(e["labels/0/value"]).toBe("Use at most 5 characters.");
    expect(e["labels/1/name"]).toBe('The key "env" is there twice.');
    expect(e["ports"]).toMatch(/^YAML line \d+: /);
  });

  it("a YAML field with a secret reference keeps the reference", () => {
    const f = fieldsOf({
      properties: { env: { type: "object", additionalProperties: true } },
    });
    expect(f[0]?.kind).toBe("yaml");
    const v = initialValues(f, { env: { DB: { secret: "db-pass" } } });
    expect(v["env"]).toBe("DB:\n  secret: db-pass\n");
    expect(toParams(f, v)).toEqual({ env: { DB: { secret: "db-pass" } } });
  });
});
