<script lang="ts">
  import { tick } from "svelte";
  import type { PlumbApi } from "../lib/client";
  import { liveSnapshot } from "../lib/live";
  import type { Tier, Token } from "../lib/types";
  import { formatTime } from "../lib/view";
  import Badge from "../ui/Badge.svelte";
  import ProblemBox from "../ui/ProblemBox.svelte";

  let { api }: { api: PlumbApi } = $props();
  let tokens = $state<Token[] | null>(null);
  let error = $state<unknown>(null);

  let name = $state("");
  let tier = $state<Tier>("plan");
  let maxRisk = $state<"reversible" | "disruptive">("reversible");
  let createError = $state<unknown>(null);
  let busy = $state(false);
  // The bearer lives only here, only until "Done". Never in a store or URL.
  let created = $state<{ name: string; bearer: string } | null>(null);
  let copied = $state(false);

  let confirmId = $state<string | null>(null);
  let revokeError = $state<unknown>(null);

  $effect(() => {
    const live = liveSnapshot({
      load: () => api.listTokens(),
      subscribe: (onEvent, onOpen) => api.events(undefined, onEvent, onOpen),
      onSnapshot: (t) => (tokens = t),
      onError: (e) => (error = e),
      onEvent: (e, l) => {
        if (e.type.startsWith("token.")) l.reloadSoon();
      },
    });
    return () => live.stop();
  });
  const reload = () =>
    api.listTokens().then(
      (t) => (tokens = t),
      (e: unknown) => (error = e),
    );

  const tierText: Record<Tier, string> = {
    read: "Read: sees the state, plans, runs and issues.",
    plan: "Plan: also proposes changes and comments on issues. It cannot approve.",
    apply: "Apply: also approves plans up to its max risk.",
  };

  async function create(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    createError = null;
    try {
      const res = await api.createToken(
        tier === "apply" ? { name, tier, max_risk: maxRisk } : { name, tier },
      );
      created = { name: res.token.name, bearer: res.bearer };
      copied = false;
      name = "";
      await reload();
      await tick();
      document.getElementById("bearer-title")?.focus();
    } catch (err) {
      createError = err;
    } finally {
      busy = false;
    }
  }

  async function copy() {
    if (!created) return;
    try {
      await navigator.clipboard.writeText(created.bearer);
      copied = true;
    } catch {
      // No clipboard access: select the text so the user can copy it.
      const el = document.getElementById("bearer-value");
      const range = document.createRange();
      if (el) range.selectNodeContents(el);
      window.getSelection()?.removeAllRanges();
      window.getSelection()?.addRange(range);
    }
  }

  function done() {
    created = null;
    copied = false;
    document.getElementById("token-name")?.focus();
  }

  async function askRevoke(id: string) {
    confirmId = id;
    revokeError = null;
    await tick();
    document.getElementById(`revoke-confirm-${id}`)?.focus();
  }

  async function revoke(id: string) {
    revokeError = null;
    try {
      await api.revokeToken(id);
      confirmId = null;
      await reload();
    } catch (err) {
      revokeError = err;
    }
  }
</script>

<div class="banner info">
  <strong>An apply token is root on the guests.</strong> A file, user or service change can give root.
  The tiers limit data loss, not privilege. Give each client its own token and revoke it when you do not
  need it.
</div>

{#if created}
  <section class="card bearer" aria-labelledby="bearer-title">
    <h2 id="bearer-title" tabindex="-1">Copy the token "{created.name}" now</h2>
    <p><strong>You cannot see it again.</strong> Plumb stores only a hash of it.</p>
    <pre class="mono value" id="bearer-value">{created.bearer}</pre>
    <div class="row">
      <button class="btn primary" type="button" onclick={copy}>Copy token</button>
      <button class="btn" type="button" onclick={done}>Done, I copied it</button>
      <span role="status" class="muted">{copied ? "Copied to the clipboard." : ""}</span>
    </div>
  </section>
{/if}

<form class="card" onsubmit={create}>
  <h2>Create a token</h2>
  <div class="field">
    <label for="token-name">Name</label>
    <input
      id="token-name"
      type="text"
      bind:value={name}
      required
      maxlength="100"
      autocomplete="off"
      aria-describedby="token-name-hint"
    />
    <span class="hint" id="token-name-hint">Who uses it, for example "CI deploy".</span>
  </div>
  <div class="field">
    <label for="token-tier">Tier</label>
    <select id="token-tier" bind:value={tier} aria-describedby="token-tier-hint">
      <option value="read">read</option>
      <option value="plan">plan</option>
      <option value="apply">apply</option>
    </select>
    <span class="hint" id="token-tier-hint">{tierText[tier]}</span>
  </div>
  {#if tier === "apply"}
    <div class="field">
      <label for="token-risk">Max risk it can approve</label>
      <select id="token-risk" bind:value={maxRisk} aria-describedby="token-risk-hint">
        <option value="reversible">reversible</option>
        <option value="disruptive">disruptive (restarts)</option>
      </select>
      <span class="hint" id="token-risk-hint">Destructive plans always need a person.</span>
    </div>
  {/if}
  <ProblemBox error={createError} />
  <button class="btn primary" type="submit" disabled={busy || !name.trim()}>Create token</button>
</form>

<h2>Tokens</h2>
<ProblemBox {error} />
<ProblemBox error={revokeError} />
{#if tokens === null && !error}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if tokens && tokens.length === 0}
  <p class="muted">No tokens yet.</p>
{:else if tokens}
  <div class="table-wrap card flush">
    <table class="stack">
      <caption class="sr-only">API tokens, newest first</caption>
      <thead>
        <tr>
          <th scope="col">Name</th>
          <th scope="col">Tier</th>
          <th scope="col">Max risk</th>
          <th scope="col">Created</th>
          <th scope="col">Last used</th>
          <th scope="col">Status</th>
        </tr>
      </thead>
      <tbody>
        {#each tokens as t (t.id)}
          <tr>
            <th scope="row">{t.name}<br /><span class="mono id">{t.id}</span></th>
            <td data-label="Tier">{t.tier}</td>
            <td data-label="Max risk">{t.max_risk ?? "–"}</td>
            <td data-label="Created"
              >{formatTime(t.created_at)}<br /><span class="mono id">{t.created_by}</span></td
            >
            <td data-label="Last used">{t.last_used_at ? formatTime(t.last_used_at) : "Never"}</td>
            <td data-label="Status">
              {#if t.revoked_at}
                <Badge tone="muted" label="Revoked" />
                <span class="id">{formatTime(t.revoked_at)}</span>
              {:else if confirmId === t.id}
                <div class="confirm" role="group" aria-label="Confirm revoke of {t.name}">
                  <span>Revoke "{t.name}"? It stops working at once.</span>
                  <div class="row">
                    <button
                      id="revoke-confirm-{t.id}"
                      class="btn danger"
                      type="button"
                      onclick={() => revoke(t.id)}>Revoke token</button
                    >
                    <button class="btn" type="button" onclick={() => (confirmId = null)}
                      >Cancel</button
                    >
                  </div>
                </div>
              {:else}
                <div class="row">
                  <Badge tone="ok" label="Active" />
                  <button class="btn" type="button" onclick={() => askRevoke(t.id)}
                    >Revoke<span class="sr-only"> {t.name}</span></button
                  >
                </div>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .bearer {
    border: 2px solid var(--color-warn);
  }
  .bearer h2:focus {
    outline: none;
  }
  .value {
    background: var(--color-surface-2);
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius);
    white-space: pre-wrap;
    word-break: break-all;
    user-select: all;
  }
  .flush {
    padding: 0;
  }
  .id {
    font-size: var(--text-xs);
    color: var(--color-text-muted);
    font-weight: 400;
  }
  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }
</style>
