import { describe, expect, it } from "vitest";
import {
  fieldsOf,
  humanize,
  initialValues,
  mergeParams,
  secretName,
  toParams,
  validate,
} from "./schemaForm";

const schema = {
  type: "object",
  required: ["server_names", "name"],
  properties: {
    name: {
      type: "string",
      title: "User name",
      default: "deploy",
      pattern: "^[a-z_][a-z0-9_-]{0,31}$",
    },
    server_names: { type: "array", items: { type: "string" }, minItems: 1 },
    port: { type: "integer", default: 8080, minimum: 1, maximum: 65535 },
    ratio: { type: "number" },
    tls: { type: "boolean", default: false },
    log_level: { type: "string", enum: ["error", "warn", "info"], default: "warn" },
    extra: { type: "string", contentMediaType: "text/plain", maxLength: 10 },
    nested: { type: "object" },
  },
};

describe("schema form", () => {
  const fields = fieldsOf(schema);

  it("maps every supported type to a widget", () => {
    expect(fields.map((f) => [f.name, f.kind, f.required])).toEqual([
      ["name", "text", true],
      ["server_names", "list", true],
      ["port", "integer", false],
      ["ratio", "number", false],
      ["tls", "boolean", false],
      ["log_level", "enum", false],
      ["extra", "textarea", false],
      ["nested", "yaml", false],
    ]);
    expect(fields[0]?.label).toBe("User name");
    expect(fields[1]?.label).toBe("Server names");
    expect(humanize("ssh_keys")).toBe("SSH keys");
    expect(humanize("ip")).toBe("IP");
  });

  it("starts with defaults and keeps given values", () => {
    expect(initialValues(fields)).toEqual({
      name: "deploy",
      server_names: [],
      port: "8080",
      ratio: "",
      tls: false,
      log_level: "warn",
      extra: "",
      nested: "",
    });
    expect(initialValues(fields, { port: 80, tls: true, server_names: ["a"] })).toMatchObject({
      port: "80",
      tls: true,
      server_names: ["a"],
    });
  });

  it("validates required, numbers, enum, length and pattern", () => {
    const values = initialValues(fields);
    values["name"] = "Bad Name";
    values["port"] = "70000";
    values["ratio"] = "x";
    values["extra"] = "far too long text";
    expect(validate(fields, values)).toEqual({
      name: "The value has the wrong format.",
      server_names: "Add at least one entry.",
      port: "Use 65535 or less.",
      ratio: "Enter a number.",
      extra: "Use at most 10 characters.",
    });
    values["port"] = "80.5";
    expect(validate(fields, values)["port"]).toBe("Enter a whole number.");
    values["name"] = "";
    expect(validate(fields, values)["name"]).toBe("This field is required.");
    values["log_level"] = "debug";
    expect(validate(fields, values)["log_level"]).toBe("Choose one of the options.");
  });

  it("builds typed parameters", () => {
    const values = initialValues(fields, { server_names: [" a.example ", "", "b.example"] });
    values["ratio"] = "0.5";
    expect(validate(fields, values)).toEqual({});
    expect(toParams(fields, values)).toEqual({
      name: "deploy",
      server_names: ["a.example", "b.example"],
      port: 8080,
      ratio: 0.5,
      tls: false,
      log_level: "warn",
    });
  });

  it("handles a missing schema", () => {
    expect(fieldsOf(undefined)).toEqual([]);
  });
});

describe("mergeParams", () => {
  const fields = fieldsOf({
    properties: {
      name: { type: "string" },
      opt: { type: "string" },
      nested: { type: "object" },
    },
  });
  it("keeps what the form cannot show and replaces what it owns", () => {
    const stored = { name: "old", opt: "x", nested: { a: 1 }, unknown_key: [1, 2] };
    const values = initialValues(fields, stored);
    values["name"] = "new";
    values["opt"] = ""; // cleared by the user: removed
    expect(mergeParams(stored, fields, values)).toEqual({
      name: "new",
      nested: { a: 1 },
      unknown_key: [1, 2],
    });
  });
});

describe("secret parameters (D-088)", () => {
  const schema = {
    required: ["db_password"],
    properties: {
      db_password: { type: "string", writeOnly: true, title: "Database password", default: "x" },
      api_key: { type: "string", writeOnly: true },
      host: { type: "string", default: "db1" },
    },
  };
  const fields = fieldsOf(schema);

  it("a writeOnly string is a secret field", () => {
    expect(fields.map((f) => f.kind)).toEqual(["secret", "secret", "text"]);
  });
  it("writeOnly wins over an enum and never saves a literal (QA T-036 F1)", () => {
    const f = fieldsOf({
      properties: {
        password: {
          type: "string",
          writeOnly: true,
          enum: ["literal-example"],
          default: "literal-example",
        },
      },
    });
    expect(f[0]?.kind).toBe("secret");
    expect(initialValues(f)).toEqual({ password: "" });
    expect(toParams(f, { password: "app-db" })).toEqual({ password: { secret: "app-db" } });
  });
  it("reads a stored reference and never a literal or default", () => {
    expect(initialValues(fields, { db_password: { secret: "app-db" } })).toEqual({
      db_password: "app-db",
      api_key: "",
      host: "db1",
    });
    expect(initialValues(fields, { db_password: "literal", api_key: { secret: 1 } })).toMatchObject(
      {
        db_password: "",
        api_key: "",
      },
    );
  });
  it("requires a secret name when required", () => {
    expect(validate(fields, initialValues(fields))).toEqual({ db_password: "Choose a secret." });
    expect(validate(fields, { db_password: "Bad Name", api_key: "", host: "" })).toEqual({
      db_password: "Choose a secret.",
    });
  });
  it("saves {secret: name} and leaves out an empty optional secret", () => {
    const params = toParams(fields, { db_password: "app-db", api_key: "", host: "db1" });
    expect(params).toEqual({ db_password: { secret: "app-db" }, host: "db1" });
  });
  it("keeps a stored reference on merge when the form owns the field", () => {
    const values = initialValues(fields, { db_password: { secret: "app-db" } });
    expect(mergeParams({ db_password: { secret: "app-db" } }, fields, values)).toEqual({
      db_password: { secret: "app-db" },
      host: "db1",
    });
  });
  it("secretName reads only a pure reference", () => {
    expect(secretName({ secret: "a" })).toBe("a");
    expect(secretName({ secret: "a", x: 1 })).toBeUndefined();
    expect(secretName("a")).toBeUndefined();
    expect(secretName(null)).toBeUndefined();
  });
});

it("accepts numeric bounds and rejects values outside them (T-098)", () => {
  const fields = fieldsOf({ properties: { n: { type: "integer", minimum: 1, maximum: 3 } } });
  for (const n of ["1", "3"]) expect(validate(fields, { n })).toEqual({});
  for (const n of ["0", "4"]) expect(validate(fields, { n }).n).toBeTruthy();
});
