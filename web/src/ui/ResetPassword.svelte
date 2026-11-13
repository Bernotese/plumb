<script lang="ts">
  // One-time password for another user (T-118; D-321, D-311): after a
  // restore every user but admin is locked. The server makes the password;
  // it is shown here once, like a token bearer, and lives only in this state.
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import ProblemBox from "./ProblemBox.svelte";

  let { api, id = "otp" }: { api: PlumbApi; id?: string } = $props();

  let name = $state("");
  let nameError = $state("");
  let asking = $state(false);
  let busy = $state(false);
  let error = $state<unknown>(null);
  let shown = $state<{ username: string; password: string } | null>(null);
  let copied = $state(false);

  async function ask(ev: SubmitEvent) {
    ev.preventDefault();
    error = null;
    const n = name.trim();
    nameError = n ? "" : "Enter the user name.";
    if (nameError) {
      document.getElementById(`${id}-name`)?.focus();
      return;
    }
    asking = true;
    await tick();
    document.getElementById(`${id}-confirm`)?.focus();
  }

  function cancel() {
    asking = false;
    document.getElementById(`${id}-name`)?.focus();
  }

  async function reset() {
    busy = true;
    error = null;
    try {
      const res = await api.resetUserPassword(name.trim());
      shown = { username: res.username, password: res.password };
      copied = false;
      asking = false;
      name = "";
      await tick();
      document.getElementById(`${id}-title`)?.focus();
    } catch (e) {
      error = e;
      asking = false;
    } finally {
      busy = false;
    }
  }

  // A shown password is lost when the page closes: the browser asks first.
  $effect(() => {
    if (!shown) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  });

  async function copy() {
    if (!shown) return;
    try {
      await navigator.clipboard.writeText(shown.password);
      copied = true;
    } catch {
      const el = document.getElementById(`${id}-value`);
      const range = document.createRange();
      if (el) range.selectNodeContents(el);
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
    }
  }

  function done() {
    shown = null;
    copied = false;
    document.getElementById(`${id}-name`)?.focus();
  }
</script>

{#if shown}
  <div class="shown" role="group" aria-labelledby="{id}-title">
    <h3 id="{id}-title" tabindex="-1">One-time password for "{shown.username}"</h3>
    <p>
      <strong>You cannot see it again.</strong> Give it to {shown.username} in a safe way. At the first
      login, {shown.username} must choose a new password.
    </p>
    <pre class="mono value" id="{id}-value">{shown.password}</pre>
    <div class="row">
      <button class="btn primary" type="button" onclick={copy}>Copy password</button>
      <button class="btn" type="button" onclick={done}>Done, I copied it</button>
      <span role="status" class="muted">{copied ? "Copied to the clipboard." : ""}</span>
    </div>
  </div>
{/if}

<form onsubmit={ask} novalidate>
  <div class="field">
    <label for="{id}-name">User name</label>
    <input
      id="{id}-name"
      type="text"
      bind:value={name}
      maxlength="64"
      autocomplete="off"
      spellcheck="false"
      aria-invalid={!!nameError}
      aria-describedby="{id}-hint{nameError ? ` ${id}-error` : ''}"
    />
    <span class="hint" id="{id}-hint"
      >Not your own user: change your own password in your account.</span
    >
    {#if nameError}<span class="error" id="{id}-error">{nameError}</span>{/if}
  </div>
  <ProblemBox {error} />
  {#if asking}
    <div
      class="confirm"
      role="alertdialog"
      aria-labelledby="{id}-ask"
      aria-describedby="{id}-ask-text"
    >
      <p id="{id}-ask"><strong>Give "{name.trim()}" a one-time password?</strong></p>
      <p id="{id}-ask-text">
        The old password stops working, and every session of this user ends. A lock from the restore
        ends too.
      </p>
      <div class="row">
        <button class="btn danger" type="button" id="{id}-confirm" disabled={busy} onclick={reset}
          >Make a one-time password</button
        >
        <button class="btn" type="button" onclick={cancel}>Cancel</button>
      </div>
    </div>
  {:else}
    <button class="btn" type="submit">Give a one-time password</button>
  {/if}
</form>

<style>
  .shown {
    border: 1px solid var(--color-border);
    border-left: 4px solid var(--color-warn);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .shown h3 {
    margin-top: 0;
  }
  .value {
    padding: var(--space-2);
    background: var(--color-surface-2);
    border-radius: var(--radius);
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }
  .confirm {
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    padding: var(--space-3);
  }
  .confirm p {
    margin: 0 0 var(--space-2);
  }
</style>
