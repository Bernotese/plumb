<script lang="ts">
  // One-time password for another user (T-118; D-321, D-311): after a
  // restore every user but admin is locked. The server makes the password;
  // it is shown here once, like a token bearer, and lives only in this state.
  // With `users` (the locked users of the restore) each user has a button;
  // without, a person types the user name.
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import ProblemBox from "./ProblemBox.svelte";

  let {
    api,
    id = "otp",
    users,
    onReset,
  }: {
    api: PlumbApi;
    id?: string;
    users?: string[];
    onReset?: (name: string) => void;
  } = $props();

  let name = $state("");
  let nameError = $state("");
  /** The user of the open confirm step. */
  let asking = $state<string | null>(null);
  let busy = $state(false);
  let error = $state<unknown>(null);
  let shown = $state<{ username: string; password: string } | null>(null);
  let copied = $state(false);

  async function ask(user: string) {
    error = null;
    asking = user;
    await tick();
    document.getElementById(`${id}-confirm`)?.focus();
  }

  async function askTyped(ev: SubmitEvent) {
    ev.preventDefault();
    const n = name.trim();
    nameError = n ? "" : "Enter the user name.";
    if (nameError) {
      document.getElementById(`${id}-name`)?.focus();
      return;
    }
    await ask(n);
  }

  /** Focus after the confirm step or the shown password closes (after the DOM update). */
  async function refocus(user: string | null) {
    await tick();
    // Ids by list index: two names never share one (review L5 of #227).
    const i = user && users ? users.indexOf(user) : -1;
    const target = users
      ? ((i >= 0 ? document.getElementById(`${id}-for-${i}`) : null) ??
        document.getElementById(`${id}-users-title`))
      : document.getElementById(`${id}-name`);
    target?.focus();
  }

  function cancel() {
    const user = asking;
    asking = null;
    void refocus(user);
  }

  async function reset() {
    const user = asking;
    if (!user) return;
    busy = true;
    error = null;
    try {
      const res = await api.resetUserPassword(user);
      shown = { username: res.username, password: res.password };
      copied = false;
      asking = null;
      name = "";
      onReset?.(res.username);
      await tick();
      document.getElementById(`${id}-title`)?.focus();
    } catch (e) {
      error = e;
      asking = null;
      void refocus(user);
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
    const user = shown?.username ?? null;
    shown = null;
    copied = false;
    void refocus(user);
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

{#snippet confirmStep(user: string)}
  <div class="confirm" role="group" aria-labelledby="{id}-ask" aria-describedby="{id}-ask-text">
    <p id="{id}-ask"><strong>Give "{user}" a one-time password?</strong></p>
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
{/snippet}

{#if users}
  <ProblemBox {error} />
  {#if users.length}
    <ul class="users" id="{id}-users" aria-labelledby="{id}-users-title">
      {#each users as u, i (u)}
        <li>
          <div class="row user">
            <span class="mono">{u}</span>
            <button
              class="btn"
              type="button"
              id="{id}-for-{i}"
              aria-label="Give {u} a one-time password"
              disabled={asking !== null}
              onclick={() => ask(u)}>One-time password</button
            >
          </div>
          {#if asking === u}{@render confirmStep(u)}{/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted" id="{id}-none">No user is locked.</p>
  {/if}
{:else}
  <form onsubmit={askTyped} novalidate>
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
      {@render confirmStep(asking)}
    {:else}
      <button class="btn" type="submit">Give a one-time password</button>
    {/if}
  </form>
{/if}

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
    margin: var(--space-2) 0;
  }
  .confirm p {
    margin: 0 0 var(--space-2);
  }
  .users {
    list-style: none;
    padding: 0;
    margin: 0 0 var(--space-3);
  }
  .users li {
    border-top: 1px solid var(--color-border);
    padding: var(--space-2) 0;
  }
  .user {
    justify-content: space-between;
    align-items: center;
  }
</style>
