// Starts `plumb serve --dev --dev-demo` for the end-to-end test and hands the
// one-time admin password to the test through the environment.
import { execFile, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { copyFile, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
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
 * admin password, the data directory and a stop function.
 * A spec can start a second, fresh server with it (export/import).
 * With `data` the server uses that directory (a restore, T-118); it prints
 * no password then. stop({ keep: true }) keeps the data directory.
 */
export async function startPlumb(port, { demo = true, data = undefined } = {}) {
  if (port === 0) port = await freePorts();
  const bin = plumbBin();
  const fresh = data === undefined;
  data ??= await mkdtemp(join(tmpdir(), "plumb-e2e-"));
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
      if ((m || !fresh) && /listens on/.test(out)) {
        clearTimeout(timer);
        ok(m?.[1] ?? "");
      }
    };
    proc.stdout.on("data", read);
    proc.stderr.on("data", read);
    proc.on("exit", (code) => fail(new Error(`plumb exited (${code}):\n${out}`)));
  });
  const stop = async ({ keep = false } = {}) => {
    if (proc.exitCode === null) {
      proc.kill("SIGTERM");
      await new Promise((done) => proc.once("exit", done));
    }
    if (!keep) await rm(data, { recursive: true, force: true });
  };
  return { base: `http://127.0.0.1:${port}`, password, data, stop };
}

function plumbBin() {
  const bin = resolve(import.meta.dirname, "../../bin/plumb");
  if (!existsSync(bin)) throw new Error(`${bin} is missing: run "make build" first`);
  return bin;
}

/**
 * Runs `plumb restore` of a backup file into a new data directory with the
 * master key of `keyData` (the dev key; T-118, D-303). restore prints the
 * new admin password only on a terminal: script(1) gives it one. Returns
 * the data directory and the new admin password. The data directory gets
 * the dev mode marker after the restore: serve --dev opens only dev data.
 */
export async function restorePlumb(keyData, backupFile) {
  const data = await mkdtemp(join(tmpdir(), "plumb-e2e-restore-"));
  await mkdir(join(data, "key"), { mode: 0o700 });
  await copyFile(join(keyData, "key", "master.key"), join(data, "key", "master.key"));
  const q = (s) => `'${s.replaceAll("'", "'\\''")}'`;
  const cmd = `${q(plumbBin())} restore --data ${q(data)} --from ${q(backupFile)}`;
  const out = await new Promise((ok, fail) =>
    execFile("script", ["-qec", cmd, "/dev/null"], { timeout: 60_000 }, (err, stdout, stderr) =>
      err
        ? fail(new Error(`plumb restore failed: ${err.message}\n${stdout}${stderr}`))
        : ok(stdout),
    ),
  );
  const m = /User: admin\s+Password: (\S+)/.exec(out);
  if (!m) throw new Error(`plumb restore printed no password:\n${out}`);
  await writeFile(join(data, "mode"), "dev\n", { mode: 0o600 });
  return { data, password: m[1] };
}

export default async function globalSetup() {
  const { password, stop } = await startPlumb(18460);
  process.env["PLUMB_E2E_PASSWORD"] = password;
  return stop;
}
