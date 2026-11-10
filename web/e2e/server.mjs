// Starts `plumb serve --dev --dev-demo` for the end-to-end test and hands the
// one-time admin password to the test through the environment.
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { createServer } from "node:net";
import { join, resolve } from "node:path";

/** A free port p on 127.0.0.1 whose neighbour p + 1 is free too. */
async function freePorts() {
  const free = (p) =>
    new Promise((ok) => {
      const s = createServer();
      s.once("error", () => ok(0));
      s.listen(p, "127.0.0.1", () => {
        const got = s.address().port;
        s.close(() => ok(got));
      });
    });
  for (let i = 0; i < 20; i++) {
    const p = await free(0);
    if (p && p < 65535 && (await free(p + 1))) return p;
  }
  throw new Error("no two free neighbouring ports");
}

/**
 * Starts a dev server with its own data directory on 127.0.0.1:<port> (agents
 * on port + 1; port 0 picks free ports). Returns the base URL, the one-time
 * admin password and a stop function.
 * A spec can start a second, fresh server with it (export/import).
 */
export async function startPlumb(port, { demo = true } = {}) {
  if (port === 0) port = await freePorts();
  const bin = resolve(import.meta.dirname, "../../bin/plumb");
  if (!existsSync(bin)) throw new Error(`${bin} is missing: run "make build" first`);
  const data = await mkdtemp(join(tmpdir(), "plumb-e2e-"));
  const args = ["serve", "--dev", ...(demo ? ["--dev-demo"] : [])];
  args.push("--data", data, "--listen", `127.0.0.1:${port}`);
  args.push("--agent-listen", `127.0.0.1:${port + 1}`);
  const proc = spawn(bin, args, { stdio: ["ignore", "pipe", "pipe"] });
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
  const stop = async () => {
    if (proc.exitCode === null) {
      proc.kill("SIGTERM");
      await new Promise((done) => proc.once("exit", done));
    }
    await rm(data, { recursive: true, force: true });
  };
  return { base: `http://127.0.0.1:${port}`, password, stop };
}

export default async function globalSetup() {
  const { password, stop } = await startPlumb(18460);
  process.env["PLUMB_E2E_PASSWORD"] = password;
  return stop;
}
