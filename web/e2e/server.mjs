// Starts `plumb serve --dev --dev-demo` for the end-to-end test and hands the
// one-time admin password to the test through the environment.
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

export default async function globalSetup() {
  const bin = resolve(import.meta.dirname, "../../bin/plumb");
  if (!existsSync(bin)) throw new Error(`${bin} is missing: run "make build" first`);
  const data = await mkdtemp(join(tmpdir(), "plumb-e2e-"));
  const proc = spawn(
    bin,
    [
      "serve",
      "--dev",
      "--dev-demo",
      "--data",
      data,
      "--listen",
      "127.0.0.1:18460",
      "--agent-listen",
      "127.0.0.1:18461",
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  let out = "";
  const password = await new Promise((ok, fail) => {
    const timer = setTimeout(() => fail(new Error(`plumb did not start:\n${out}`)), 30_000);
    const read = (chunk) => {
      out += chunk;
      const m = /User: admin\s+Password: (\S+)/.exec(out);
      if (m && /listens on/.test(out)) {
        clearTimeout(timer);
        ok(m[1]);
      }
    };
    proc.stdout.on("data", read);
    proc.stderr.on("data", read);
    proc.on("exit", (code) => fail(new Error(`plumb exited (${code}):\n${out}`)));
  });
  process.env["PLUMB_E2E_PASSWORD"] = password;
  return async () => {
    proc.kill("SIGTERM");
  };
}
