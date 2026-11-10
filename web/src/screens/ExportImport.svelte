<script lang="ts">
  import { ApiError } from "../lib/api";
  import type { PlumbApi } from "../lib/client";
  import { saveText } from "../lib/download";
  import { go, href } from "../lib/router";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api, save = saveText }: { api: PlumbApi; save?: (filename: string, text: string) => void } =
    $props();

  const MAX_BYTES = 8 * 1024 * 1024;
  const MIN_PASS = 12;

  // Export. Passphrases are cleared after every request.
  let withSecrets = $state(false);
  let pass1 = $state("");
  let pass2 = $state("");
  let exportError = $state<unknown>(null);
  let exportDone = $state<string | null>(null);
  let exporting = $state(false);
  const passShort = $derived(withSecrets && pass1.length > 0 && pass1.length < MIN_PASS);
  const passDiffer = $derived(withSecrets && pass2.length > 0 && pass1 !== pass2);
  const exportReady = $derived(!withSecrets || (pass1.length >= MIN_PASS && pass1 === pass2));

  async function doExport(e: SubmitEvent) {
    e.preventDefault();
    if (!exportReady) return;
    exporting = true;
    exportError = null;
    exportDone = null;
    const pass = withSecrets ? pass1 : undefined;
    pass1 = "";
    pass2 = "";
    try {
      const text = await api.exportState(pass);
      const day = new Date().toISOString().slice(0, 10);
      const file = `plumb-export-${day}${pass ? "-with-secrets" : ""}.yaml`;
      save(file, text);
      exportDone = file;
    } catch (err) {
      exportError = err;
    } finally {
      exporting = false;
    }
  }

  // Import.
  let fileName = $state<string | null>(null);
  let content = $state<string | null>(null);
  let fileError = $state<string | null>(null);
  let mode = $state<"replace" | "merge">("replace");
  let passphrase = $state("");
  let reason = $state("");
  let importError = $state<unknown>(null);
  let importing = $state(false);
  /** Names of a 409 secret-conflict; the user ticks the ones to overwrite. */
  let conflicts = $state<string[] | null>(null);
  let overwrite = $state<string[]>([]);
  /** Names of a 422 about missing secrets. */
  let missing = $state<string[] | null>(null);

  async function pick(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    const f = input.files?.[0];
    content = null;
    fileName = null;
    fileError = null;
    conflicts = null;
    overwrite = [];
    missing = null;
    importError = null;
    if (!f) return;
    if (f.size > MAX_BYTES) {
      fileError = "The file is larger than 8 MiB. Plumb does not accept it.";
      return;
    }
    fileName = f.name;
    content = await f.text();
  }

  async function doImport(e: SubmitEvent) {
    e.preventDefault();
    if (!content || !reason.trim()) return;
    importing = true;
    importError = null;
    missing = null;
    try {
      const plan = await api.importState({
        content,
        mode,
        reason: reason.trim(),
        ...(passphrase ? { passphrase } : {}),
        ...(overwrite.length ? { overwrite } : {}),
      });
      passphrase = "";
      content = null;
      go({ name: "plan", id: plan.id });
    } catch (err) {
      const p = err instanceof ApiError ? err.problem : undefined;
      if (p?.type === "urn:plumb:problem:secret-conflict" && p.secrets?.length) {
        conflicts = p.secrets;
        overwrite = overwrite.filter((n) => p.secrets?.includes(n));
      } else if (p?.status === 422 && p.secrets?.length) {
        missing = p.secrets;
      } else {
        importError = err;
      }
    } finally {
      importing = false;
    }
  }
</script>

<form class="card" onsubmit={doExport} autocomplete="off">
  <h2>Export</h2>
  <p>
    One YAML file with every image, block, target and rule of the head revision. History, issues,
    runs, users and tokens are not in it.
  </p>
  <fieldset>
    <legend>Secret values</legend>
    <label class="choice">
      <input type="radio" name="export-secrets" value={false} bind:group={withSecrets} />
      <span>Without secret values. The file lists only the secret names.</span>
    </label>
    <label class="choice">
      <input type="radio" name="export-secrets" value={true} bind:group={withSecrets} />
      <span>With secret values, encrypted with a passphrase. You need it to import them again.</span
      >
    </label>
  </fieldset>
  {#if withSecrets}
    <div class="field">
      <label for="export-pass">Passphrase</label>
      <input
        id="export-pass"
        type="password"
        bind:value={pass1}
        autocomplete="new-password"
        aria-invalid={passShort}
        aria-describedby="export-pass-hint"
      />
      <span class="hint" id="export-pass-hint" class:error={passShort}>
        At least {MIN_PASS} characters. Plumb cannot recover it.
      </span>
    </div>
    <div class="field">
      <label for="export-pass2">Passphrase again</label>
      <input
        id="export-pass2"
        type="password"
        bind:value={pass2}
        autocomplete="new-password"
        aria-invalid={passDiffer}
        aria-describedby="export-pass2-hint"
      />
      <span class="hint" id="export-pass2-hint" class:error={passDiffer}>
        {passDiffer ? "The two passphrases differ." : "Type it again to check it."}
      </span>
    </div>
  {/if}
  <ProblemBox error={exportError} />
  {#if exportDone}
    <p class="banner ok" role="status">Saved as <span class="mono">{exportDone}</span>.</p>
  {/if}
  <button class="btn primary" type="submit" disabled={exporting || !exportReady}>
    {exporting ? "Exporting…" : "Download export"}
  </button>
</form>

<form class="card" onsubmit={doImport} autocomplete="off">
  <h2>Import</h2>
  <p>
    Plumb reads an export file and makes a plan. Nothing changes until you approve the plan, except
    secrets: they are written at once (see below).
  </p>
  <div class="field">
    <label for="import-file">Export file</label>
    <input
      id="import-file"
      type="file"
      accept=".yaml,.yml,application/yaml,text/yaml"
      onchange={pick}
      aria-describedby="import-file-hint"
    />
    <span class="hint" id="import-file-hint" class:error={fileError}>
      {fileError ?? (fileName ? `Selected: ${fileName}` : "A file from Export, at most 8 MiB.")}
    </span>
  </div>
  <fieldset>
    <legend>Mode</legend>
    <label class="choice">
      <input type="radio" name="import-mode" value="replace" bind:group={mode} />
      <span
        ><strong>Replace</strong>: the state becomes the file. Images, blocks, targets and rules
        that are not in the file are deleted.</span
      >
    </label>
    <label class="choice">
      <input type="radio" name="import-mode" value="merge" bind:group={mode} />
      <span
        ><strong>Merge</strong>: Plumb adds and updates the documents of the file and keeps all
        others.</span
      >
    </label>
  </fieldset>
  <div class="field">
    <label for="import-pass">Passphrase of the secret bundle (optional)</label>
    <input
      id="import-pass"
      type="password"
      bind:value={passphrase}
      autocomplete="off"
      aria-describedby="import-pass-hint"
    />
    <span class="hint" id="import-pass-hint">
      Without it, Plumb writes no secret from the file. With it, Plumb creates the missing secrets
      at once; they stay when you drop the plan.
    </span>
  </div>
  <div class="field">
    <label for="import-reason">Reason (required)</label>
    <textarea id="import-reason" bind:value={reason} required maxlength="2000"></textarea>
  </div>

  {#if missing}
    <div class="banner danger" role="alert">
      <strong>The file refers to secrets that do not exist:</strong>
      <span class="mono">{missing.join(", ")}</span>.
      <a href={href({ name: "settings", tab: "secrets" })}>Create them on the secrets page</a>, or
      give the passphrase of a file that holds them.
    </div>
  {/if}

  {#if conflicts}
    <fieldset class="conflicts" aria-describedby="conflict-hint">
      <legend>Secrets with another value</legend>
      <p id="conflict-hint" role="alert">
        These secrets exist in Plumb with another value. Nothing was written. Tick each secret that
        the file may overwrite, then import again. The old values are not kept.
      </p>
      {#each conflicts as n (n)}
        <label class="choice">
          <input type="checkbox" value={n} bind:group={overwrite} />
          <span>Overwrite <span class="mono">{n}</span></span>
        </label>
      {/each}
    </fieldset>
  {/if}

  <ProblemBox error={importError} />
  <button class="btn primary" type="submit" disabled={importing || !content || !reason.trim()}>
    {importing ? "Importing…" : conflicts ? "Import again" : "Make import plan"}
  </button>
</form>

<style>
  .choice {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    margin: var(--space-1) 0;
    font-weight: 400;
  }
  legend {
    font-weight: 600;
    font-size: var(--text-sm);
  }
  .hint.error {
    color: var(--color-danger);
  }
  .conflicts {
    border-color: var(--color-warn);
  }
  .conflicts p {
    margin-top: 0;
  }
</style>
